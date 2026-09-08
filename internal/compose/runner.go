// Package compose orchestrates a compose project on top of the container
// runtime: creating networks and volumes, translating services into
// containers, ordering them by dependency, and keeping service names
// resolvable through generated hosts files.
package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// Labels private to apple-compose, stored on containers so file-less commands
// such as `down -p name` can honour per-service stop settings.
const (
	LabelStopSignal = "com.apple-compose.stop-signal"
	LabelStopGrace  = "com.apple-compose.stop-grace"
	LabelHostsFile  = "com.apple-compose.hosts-file"
)

// DefaultStopTimeout mirrors Docker Compose's ten second grace period.
const DefaultStopTimeout = 10 * time.Second

// Runner executes compose operations for one project.
type Runner struct {
	Engine  *engine.Engine
	Project *types.Project
	Console *ui.Console
	Version string

	warnMu sync.Mutex
	warned map[string]bool

	// exits holds the exit channels of containers this process is attached
	// to, so waits can use the real exit code instead of the recorded one.
	exitsMu sync.Mutex
	exits   map[string]chan int
}

// trackExit registers an in-flight attached container.
func (r *Runner) trackExit(name string) chan int {
	r.exitsMu.Lock()
	defer r.exitsMu.Unlock()
	if r.exits == nil {
		r.exits = map[string]chan int{}
	}
	ch := make(chan int, 1)
	r.exits[name] = ch
	return ch
}

// trackedExit returns the exit channel of an attached container, if any.
func (r *Runner) trackedExit(name string) (chan int, bool) {
	r.exitsMu.Lock()
	defer r.exitsMu.Unlock()
	ch, ok := r.exits[name]
	return ch, ok
}

// New builds a Runner.
func New(e *engine.Engine, p *types.Project, c *ui.Console, version string) *Runner {
	return &Runner{Engine: e, Project: p, Console: c, Version: version, warned: map[string]bool{}}
}

// warnOnce prints a warning a single time per process.
func (r *Runner) warnOnce(key, format string, args ...any) {
	r.warnMu.Lock()
	defer r.warnMu.Unlock()
	if r.warned[key] {
		return
	}
	r.warned[key] = true
	r.Console.Warn(format, args...)
}

// containers returns the project's containers, optionally including stopped ones.
func (r *Runner) containers(ctx context.Context, all bool) ([]engine.Container, error) {
	return r.Engine.ProjectContainers(ctx, r.Project.Name, all)
}

// serviceContainers filters containers to the named services (all when empty),
// excluding one-off containers unless oneOff is set.
func serviceContainers(cs []engine.Container, services []string, oneOff bool) []engine.Container {
	want := map[string]bool{}
	for _, s := range services {
		want[s] = true
	}
	var out []engine.Container
	for _, c := range cs {
		if !oneOff && c.Label(project.LabelOneOff) == "True" {
			continue
		}
		if len(want) > 0 && !want[c.Label(project.LabelService)] {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Label(project.LabelService) != b.Label(project.LabelService) {
			return a.Label(project.LabelService) < b.Label(project.LabelService)
		}
		return containerNumber(a) < containerNumber(b)
	})
	return out
}

func containerNumber(c engine.Container) int {
	n, _ := strconv.Atoi(c.Label(project.LabelContainerNumber))
	return n
}

func ids(cs []engine.Container) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// stopGroups groups containers by their stop signal and grace period so each
// group can be stopped with a single runtime call.
type stopGroup struct {
	signal  string
	timeout time.Duration
	ids     []string
}

func stopGroups(cs []engine.Container, override time.Duration) []stopGroup {
	byKey := map[string]*stopGroup{}
	var order []string
	for _, c := range cs {
		timeout := DefaultStopTimeout
		if override > 0 {
			timeout = override
		} else if v := c.Label(LabelStopGrace); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				timeout = d
			}
		}
		sig := c.Label(LabelStopSignal)
		key := sig + "/" + timeout.String()
		g, ok := byKey[key]
		if !ok {
			g = &stopGroup{signal: sig, timeout: timeout}
			byKey[key] = g
			order = append(order, key)
		}
		g.ids = append(g.ids, c.ID)
	}
	out := make([]stopGroup, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// stopContainers stops running containers honouring per-service settings and
// reports each one.
func (r *Runner) stopContainers(ctx context.Context, cs []engine.Container, override time.Duration) error {
	var running []engine.Container
	for _, c := range cs {
		if c.Running() {
			running = append(running, c)
		}
	}
	var errs []error
	for _, g := range stopGroups(running, override) {
		if err := r.Engine.Stop(ctx, g.ids, g.signal, g.timeout); err != nil {
			errs = append(errs, err)
			for _, id := range g.ids {
				r.Console.Fail("Container", id, "Stopping", err)
			}
			continue
		}
		for _, id := range g.ids {
			r.Console.Step("Container", id, "Stopped")
		}
	}
	return errors.Join(errs...)
}

// removeContainers deletes containers and reports each one.
func (r *Runner) removeContainers(ctx context.Context, cs []engine.Container, force bool) error {
	if len(cs) == 0 {
		return nil
	}
	if err := r.Engine.Delete(ctx, ids(cs), force); err != nil {
		for _, c := range cs {
			r.Console.Fail("Container", c.ID, "Removing", err)
		}
		return err
	}
	for _, c := range cs {
		r.Console.Step("Container", c.ID, "Removed")
	}
	return nil
}

// serviceOrder returns service names in dependency order (dependencies first).
func serviceOrder(p *types.Project, names []string) []string {
	visited := map[string]bool{}
	var out []string
	var visit func(string)
	visit = func(n string) {
		if visited[n] {
			return
		}
		visited[n] = true
		if s, err := p.GetService(n); err == nil {
			deps := s.GetDependencies()
			sort.Strings(deps)
			for _, d := range deps {
				if _, ok := p.Services[d]; ok {
					visit(d)
				}
			}
		}
		out = append(out, n)
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, n := range sorted {
		visit(n)
	}
	return out
}

// reverse returns a reversed copy.
func reverse(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// orderContainers sorts containers so dependencies come first (or last when
// reversed), with orphans at the end.
func orderContainers(p *types.Project, cs []engine.Container, reversed bool) []engine.Container {
	order := serviceOrder(p, p.ServiceNames())
	if reversed {
		order = reverse(order)
	}
	rank := map[string]int{}
	for i, n := range order {
		rank[n] = i
	}
	out := append([]engine.Container(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := rank[out[i].Label(project.LabelService)]
		rj, okj := rank[out[j].Label(project.LabelService)]
		if oki != okj {
			return oki
		}
		if ri != rj {
			return ri < rj
		}
		return containerNumber(out[i]) < containerNumber(out[j])
	})
	return out
}

// waitRunning polls until the container reports running with an address.
func (r *Runner) waitRunning(ctx context.Context, id string, timeout time.Duration) (*engine.Container, error) {
	deadline := time.Now().Add(timeout)
	for {
		c, err := r.Engine.InspectContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		if c.Running() && (len(c.Configuration.Networks) == 0 || c.PrimaryIP() != "") {
			return c, nil
		}
		if c.Status.State == "stopped" && !c.Status.StartedDate.IsZero() {
			return c, nil
		}
		if time.Now().After(deadline) {
			return c, fmt.Errorf("container %s did not reach the running state within %s", id, timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// discard is an io.Writer that drops everything.
var discard io.Writer = io.Discard

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
