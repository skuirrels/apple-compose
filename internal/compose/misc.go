package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// Start starts existing stopped containers for the given services.
func (r *Runner) Start(ctx context.Context, services []string) error {
	cs, err := r.containers(ctx, true)
	if err != nil {
		return err
	}
	cs = orderContainers(r.Project, serviceContainers(cs, services, false), false)
	if len(cs) == 0 {
		return fmt.Errorf("no containers to start; run `up` first")
	}
	for _, c := range cs {
		if c.Running() {
			r.Console.Step("Container", c.ID, "Running")
			continue
		}
		clearStopped(r.Project.Name, []string{c.ID})
		if err := r.Engine.Start(ctx, c.ID); err != nil {
			r.Console.Fail("Container", c.ID, "Starting", err)
			return err
		}
		if _, err := r.waitRunning(ctx, c.ID, 2*time.Minute); err != nil {
			return err
		}
		r.Console.Step("Container", c.ID, "Started")
		if err := r.RefreshHosts(ctx); err != nil {
			return err
		}
	}
	return r.EnsureSupervisor(ctx)
}

// Stop stops running containers for the given services.
func (r *Runner) Stop(ctx context.Context, services []string, timeout time.Duration) error {
	cs, err := r.containers(ctx, false)
	if err != nil {
		return err
	}
	cs = orderContainers(r.Project, serviceContainers(cs, services, false), true)
	return r.stopContainers(ctx, cs, timeout)
}

// Restart stops and starts containers for the given services.
func (r *Runner) Restart(ctx context.Context, services []string, timeout time.Duration, noDeps bool) error {
	if !noDeps && len(services) > 0 {
		set := map[string]bool{}
		for _, s := range services {
			set[s] = true
			if svc, err := r.Project.GetService(s); err == nil {
				for _, d := range svc.GetDependents(r.Project) {
					set[d] = true
				}
			}
		}
		services = sortedKeys(set)
	}
	if err := r.Stop(ctx, services, timeout); err != nil {
		return err
	}
	return r.Start(ctx, services)
}

// Rm removes stopped containers; with stop, running ones are stopped first.
func (r *Runner) Rm(ctx context.Context, services []string, force, stop, volumes bool) error {
	cs, err := r.containers(ctx, true)
	if err != nil {
		return err
	}
	cs = orderContainers(r.Project, serviceContainers(cs, services, false), true)
	if stop {
		if err := r.stopContainers(ctx, cs, 0); err != nil {
			return err
		}
	}
	var targets []engine.Container
	for _, c := range cs {
		if c.Running() && !stop && !force {
			r.Console.Warn("container %s is running; use --stop or --force", c.ID)
			continue
		}
		targets = append(targets, c)
	}
	if err := r.removeContainers(ctx, targets, true); err != nil {
		return err
	}
	if volumes {
		r.Console.Warn("anonymous volumes are not tracked per container by the runtime; use `container volume prune` to reclaim them")
	}
	return nil
}

// Kill signals running containers.
func (r *Runner) Kill(ctx context.Context, services []string, signal string) error {
	cs, err := r.containers(ctx, false)
	if err != nil {
		return err
	}
	cs = serviceContainers(cs, services, false)
	if err := r.Engine.Kill(ctx, ids(cs), signal); err != nil {
		return err
	}
	markStopped(r.Project.Name, ids(cs))
	for _, c := range cs {
		r.Console.Step("Container", c.ID, "Killed")
	}
	return nil
}

// Port prints the host binding of a container port.
func (r *Runner) Port(ctx context.Context, service string, index int, port int, protocol string) (string, error) {
	c, err := r.findContainer(ctx, service, index, true)
	if err != nil {
		return "", err
	}
	if protocol == "" {
		protocol = "tcp"
	}
	for _, p := range c.Configuration.PublishedPorts {
		if p.ContainerPort == port && strings.EqualFold(p.Proto, protocol) {
			addr := p.HostAddress
			if addr == "" {
				addr = "0.0.0.0"
			}
			return fmt.Sprintf("%s:%d", addr, p.HostPort), nil
		}
	}
	return "", fmt.Errorf("no published port %d/%s for service %s", port, protocol, service)
}

// Wait blocks until the given services' containers stop and returns the
// first non-zero recorded exit code, or zero.
func (r *Runner) Wait(ctx context.Context, services []string, downProject bool) (int, error) {
	code := 0
	for {
		cs, err := r.containers(ctx, true)
		if err != nil {
			return 1, err
		}
		cs = serviceContainers(cs, services, false)
		running := false
		for _, c := range cs {
			if c.Running() {
				running = true
				break
			}
		}
		if !running {
			for _, c := range cs {
				if ec, ok := r.recordedExit(c.ID); ok && ec != 0 && code == 0 {
					code = ec
				}
				fmt.Fprintf(r.Console.Out, "container %q exited with status code %d\n", c.ID, exitOrZero(r, c.ID))
			}
			break
		}
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if downProject {
		if err := r.Down(ctx, DownOptions{}); err != nil {
			return 1, err
		}
	}
	return code, nil
}

func exitOrZero(r *Runner, id string) int {
	c, _ := r.recordedExit(id)
	return c
}

// Images lists the images used by the project's containers.
func (r *Runner) Images(ctx context.Context, services []string, quiet bool, format string) error {
	cs, err := r.containers(ctx, true)
	if err != nil {
		return err
	}
	cs = serviceContainers(cs, services, false)
	imgs, err := r.Engine.ListImages(ctx)
	if err != nil {
		return err
	}
	byName := map[string]engine.Image{}
	for _, img := range imgs {
		byName[img.Name()] = img
	}
	type row struct {
		Container  string `json:"ContainerName"`
		Repository string `json:"Repository"`
		Tag        string `json:"Tag"`
		ID         string `json:"ID"`
		Size       int64  `json:"Size"`
	}
	var rows []row
	for _, c := range cs {
		ref := c.Configuration.Image.Reference
		repo, tag := splitRef(ref)
		rw := row{Container: c.ID, Repository: displayImage(repo), Tag: tag}
		if img, ok := byName[ref]; ok {
			rw.ID = strings.TrimPrefix(img.ID, "sha256:")
			if len(rw.ID) > 12 {
				rw.ID = rw.ID[:12]
			}
			for _, v := range img.Variants {
				rw.Size += v.Size
			}
		}
		rows = append(rows, rw)
	}
	switch {
	case quiet:
		seen := map[string]bool{}
		for _, rw := range rows {
			if rw.ID != "" && !seen[rw.ID] {
				seen[rw.ID] = true
				fmt.Fprintln(r.Console.Out, rw.ID)
			}
		}
	case format == "json":
		if rows == nil {
			rows = []row{}
		}
		enc := json.NewEncoder(r.Console.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	default:
		var table [][]string
		for _, rw := range rows {
			table = append(table, []string{rw.Container, rw.Repository, rw.Tag, rw.ID, humanBytes(rw.Size)})
		}
		ui.Table(r.Console.Out, []string{"CONTAINER", "REPOSITORY", "TAG", "IMAGE ID", "SIZE"}, table)
	}
	return nil
}

func splitRef(ref string) (string, string) {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return strconv.FormatInt(b, 10) + "B"
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// Copy transfers files between the host and a service container.
func (r *Runner) Copy(ctx context.Context, src, dst string, index int) error {
	resolve := func(p string) (string, error) {
		svc, rest, found := strings.Cut(p, ":")
		if !found || strings.HasPrefix(p, "/") || strings.HasPrefix(p, ".") {
			return p, nil
		}
		c, err := r.findContainer(ctx, svc, index, true)
		if err != nil {
			return "", err
		}
		return c.ID + ":" + rest, nil
	}
	s, err := resolve(src)
	if err != nil {
		return err
	}
	d, err := resolve(dst)
	if err != nil {
		return err
	}
	return r.Engine.Copy(ctx, s, d)
}

// ProjectSummary describes one compose project known to the runtime.
type ProjectSummary struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

// ListProjects discovers projects from container labels.
func ListProjects(ctx context.Context, e *engine.Engine, all bool) ([]ProjectSummary, error) {
	cs, err := e.ListContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	type counts struct {
		running, exited int
		files           string
	}
	byProject := map[string]*counts{}
	for _, c := range cs {
		name := c.Label(project.LabelProject)
		if name == "" {
			continue
		}
		cnt := byProject[name]
		if cnt == nil {
			cnt = &counts{}
			byProject[name] = cnt
		}
		if c.Running() {
			cnt.running++
		} else {
			cnt.exited++
		}
		if f := c.Label(project.LabelConfigFiles); f != "" {
			cnt.files = f
		}
	}
	var out []ProjectSummary
	for _, name := range sortedKeys(byProject) {
		cnt := byProject[name]
		if !all && cnt.running == 0 {
			continue
		}
		var parts []string
		if cnt.running > 0 {
			parts = append(parts, fmt.Sprintf("running(%d)", cnt.running))
		}
		if cnt.exited > 0 {
			parts = append(parts, fmt.Sprintf("exited(%d)", cnt.exited))
		}
		out = append(out, ProjectSummary{Name: name, Status: strings.Join(parts, ", "), ConfigFiles: cnt.files})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
