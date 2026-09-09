package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/compose-spec/compose-go/v2/graph"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/state"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// UpOptions configure Up.
type UpOptions struct {
	Detach             bool
	Build              bool
	NoBuild            bool
	ForceRecreate      bool
	NoRecreate         bool
	AlwaysRecreateDeps bool
	NoStart            bool
	RemoveOrphans      bool
	Pull               PullPolicy
	QuietPull          bool
	Wait               bool
	WaitTimeout        time.Duration
	Timeout            time.Duration
	AbortOnExit        bool
	ExitCodeFrom       string
	Attach             []string
	NoAttach           []string
	AttachDependencies bool
	NoLogPrefix        bool
	NoDeps             bool
	Scale              map[string]int
	// Parallelism bounds how many services start at once. VM boots are
	// heavy, so the default is modest.
	Parallelism int
	// Services names the services given on the command line; without
	// AttachDependencies only these are attached to, as Docker does.
	Services []string
	// startTimeout bounds how long a container may take to report running.
	StartTimeout time.Duration
}

// startRecord tracks a container started during this Up.
type startRecord struct {
	container *engine.Container
	service   types.ServiceConfig
	started   time.Time
	// attach is the attached start process when running in the foreground.
	exit chan int
}

// Up creates and starts the project. It returns the process exit code the
// command should finish with.
func (r *Runner) Up(ctx context.Context, o UpOptions) (int, error) {
	if o.Parallelism <= 0 {
		o.Parallelism = 4
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = 2 * time.Minute
	}
	if o.ExitCodeFrom != "" {
		o.AbortOnExit = true
		if _, err := r.Project.GetService(o.ExitCodeFrom); err != nil {
			return 1, fmt.Errorf("--exit-code-from: %w", err)
		}
	}
	if err := r.Engine.CheckRunning(ctx); err != nil {
		return 1, err
	}
	if err := r.Project.CheckContainerNameUnicity(); err != nil {
		return 1, err
	}
	if err := r.EnsureNetworks(ctx); err != nil {
		return 1, err
	}
	// Images come before volumes: a fresh volume is emptied with the first
	// image that mounts it.
	imgOpts := ImageOptions{Build: o.Build, NoBuild: o.NoBuild, Pull: o.Pull, Quiet: o.QuietPull}
	for _, name := range serviceOrder(r.Project, r.Project.ServiceNames()) {
		s, _ := r.Project.GetService(name)
		if err := r.EnsureImage(ctx, s, imgOpts); err != nil {
			return 1, err
		}
	}
	if err := r.EnsureVolumes(ctx); err != nil {
		return 1, err
	}
	r.warnSharedVolumes()

	existing, err := r.containers(ctx, true)
	if err != nil {
		return 1, err
	}
	byName := map[string]engine.Container{}
	for _, c := range existing {
		byName[c.ID] = c
	}

	attached := !o.Detach || o.AbortOnExit
	var (
		mu      sync.Mutex
		records []*startRecord
		logs    *ui.PrefixSet
		out     io.Writer = r.Console.Out
	)
	if attached {
		logs = ui.NewPrefixSet(r.Console, out, r.attachNames(o), o.NoLogPrefix)
	}

	recreated := map[string]bool{}
	// The graph walk cancels its context when it returns, so long-lived
	// attach processes must hang off the caller's context instead.
	bg := ctx
	visit := func(ctx context.Context, name string, s types.ServiceConfig) error {
		if err := r.waitDependencies(ctx, s, o.Scale); err != nil {
			return err
		}
		hash, err := ServiceHash(s)
		if err != nil {
			return err
		}
		replicas := s.GetScale()
		if n, ok := o.Scale[name]; ok {
			replicas = n
		}
		if s.ContainerName != "" && replicas > 1 {
			return fmt.Errorf("service %s: container_name cannot be used with more than one replica", name)
		}
		// Retire surplus replicas.
		for _, c := range existing {
			if c.Label(project.LabelService) != name || c.Label(project.LabelOneOff) == "True" {
				continue
			}
			if containerNumber(c) > replicas {
				if err := r.stopContainers(ctx, []engine.Container{c}, o.Timeout); err != nil {
					return err
				}
				if err := r.removeContainers(ctx, []engine.Container{c}, true); err != nil {
					return err
				}
			}
		}
		depRecreated := false
		if o.AlwaysRecreateDeps {
			for _, d := range s.GetDependencies() {
				mu.Lock()
				depRecreated = depRecreated || recreated[d]
				mu.Unlock()
			}
		}
		for n := 1; n <= replicas; n++ {
			cname := project.ContainerName(r.Project, s, n)
			cur, exists := byName[cname]
			action := "create"
			if exists {
				switch {
				case o.NoRecreate:
					action = "keep"
				case o.ForceRecreate || depRecreated || cur.Label(project.LabelConfigHash) != hash:
					action = "recreate"
				default:
					action = "keep"
				}
			}
			rec, err := r.applyContainer(ctx, bg, s, n, cname, hash, action, &cur, exists, o, logs)
			if err != nil {
				return err
			}
			if action == "recreate" {
				mu.Lock()
				recreated[name] = true
				mu.Unlock()
			}
			if rec != nil {
				mu.Lock()
				records = append(records, rec)
				mu.Unlock()
			}
		}
		return nil
	}
	if err := graph.InDependencyOrder(ctx, r.Project, visit, graph.WithMaxConcurrency(o.Parallelism)); err != nil {
		return 1, err
	}

	if err := r.handleOrphans(ctx, existing, o.RemoveOrphans, o.Timeout); err != nil {
		return 1, err
	}

	if o.Wait {
		if err := r.waitAllHealthy(ctx, o.WaitTimeout); err != nil {
			return 1, err
		}
	}
	if o.NoStart {
		return 0, nil
	}
	if !attached {
		return 0, r.EnsureSupervisor(ctx)
	}
	return r.attachPhase(ctx, o, records, logs)
}

// applyContainer brings one replica to the desired state.
func (r *Runner) applyContainer(ctx, bg context.Context, s types.ServiceConfig, number int, name, hash, action string, cur *engine.Container, exists bool, o UpOptions, logs *ui.PrefixSet) (*startRecord, error) {
	attached := logs != nil
	switch action {
	case "keep":
		if cur.Running() {
			r.Console.Step("Container", name, "Running")
			if err := r.RefreshHosts(ctx); err != nil {
				return nil, err
			}
			if attached {
				return r.followExisting(bg, s, cur, logs, o), nil
			}
			return nil, nil
		}
		if o.NoStart {
			r.Console.Step("Container", name, "Created")
			return nil, nil
		}
		return r.startContainer(ctx, bg, s, name, o, logs, "Started")
	case "recreate":
		if cur.Running() {
			if err := r.stopContainers(ctx, []engine.Container{*cur}, o.Timeout); err != nil {
				return nil, err
			}
		}
		if err := r.Engine.Delete(ctx, []string{name}, true); err != nil {
			return nil, err
		}
		if err := r.createContainer(ctx, s, number, name, hash); err != nil {
			return nil, err
		}
		if o.NoStart {
			r.Console.Step("Container", name, "Recreated")
			return nil, nil
		}
		return r.startContainer(ctx, bg, s, name, o, logs, "Recreated")
	default:
		if err := r.createContainer(ctx, s, number, name, hash); err != nil {
			return nil, err
		}
		if o.NoStart {
			r.Console.Step("Container", name, "Created")
			return nil, nil
		}
		return r.startContainer(ctx, bg, s, name, o, logs, "Started")
	}
}

// createContainer translates and creates one replica.
func (r *Runner) createContainer(ctx context.Context, s types.ServiceConfig, number int, name, hash string) error {
	hostsPath, err := r.hostsPathFor(name)
	if err != nil {
		return err
	}
	args, err := r.createArgs(createSpec{service: s, name: name, number: number, hash: hash, hostsPath: hostsPath})
	if err != nil {
		return err
	}
	if _, err := r.Engine.Create(ctx, args...); err != nil {
		r.Console.Fail("Container", name, "Creating", err)
		return err
	}
	if !r.Engine.DryRun {
		clearStopped(r.Project.Name, []string{name})
		clearRestarts(r.Project.Name, []string{name})
	}
	// Peers that are already running must be resolvable from the very
	// first instruction of the new container, so its hosts file is filled
	// in before it starts; its own address is added once it is running.
	if err := r.RefreshHosts(ctx, name); err != nil {
		return err
	}
	return nil
}

// startHint decorates runtime start failures whose cause is well known.
func startHint(err error) error {
	if err != nil && strings.Contains(err.Error(), "storage device attachment is invalid") {
		return fmt.Errorf("%w (a named volume is already attached to another running container; the runtime attaches each volume to one container at a time)", err)
	}
	return err
}

// startContainer starts a created container, detached or attached, and waits
// for it to report an address so peers can be told about it.
func (r *Runner) startContainer(ctx, bg context.Context, s types.ServiceConfig, name string, o UpOptions, logs *ui.PrefixSet, verb string) (*startRecord, error) {
	rec := &startRecord{service: s, started: time.Now()}
	if r.Engine.DryRun {
		_, _ = r.Engine.Mutate(ctx, "start", name)
		r.Console.Step("Container", name, verb)
		return nil, nil
	}
	clearStopped(r.Project.Name, []string{name})
	switch {
	case logs != nil:
		rec.exit = make(chan int, 1)
		w := logs.Writer(s.Name)
		// The foreground session restarts this container itself; the
		// project's supervisor, if any, must leave it alone meanwhile.
		markForeground(r.Project.Name, []string{name})
		go r.attachLoop(bg, s, name, w, rec.exit, o)
	case r.exitCodeNeeded(s.Name):
		// A dependent waits for this container to complete successfully,
		// and the runtime only reports exit codes to an attached client.
		done := r.trackExit(name)
		go func() {
			code, err := r.Engine.AttachBackground(bg, name, discard, discard)
			if err != nil {
				code = 1
			}
			r.recordExit(name, code)
			done <- code
		}()
	default:
		if err := startHint(r.Engine.Start(ctx, name)); err != nil {
			r.Console.Fail("Container", name, "Starting", err)
			return nil, err
		}
	}
	c, err := r.waitRunning(ctx, name, o.StartTimeout)
	if err != nil {
		r.Console.Fail("Container", name, "Starting", err)
		return nil, err
	}
	rec.container = c
	r.Console.Step("Container", name, verb)
	if err := r.RefreshHosts(ctx); err != nil {
		return nil, err
	}
	return rec, nil
}

// attachLoop runs `container start --attach` for a container, re-running it
// when the service's restart policy asks for it, and reports the final exit
// code on done.
func (r *Runner) attachLoop(ctx context.Context, s types.ServiceConfig, name string, w io.Writer, done chan<- int, o UpOptions) {
	tracked := r.trackExit(name)
	defer clearForeground(r.Project.Name, []string{name})
	for restarted := false; ; restarted = true {
		if restarted {
			// Peers must learn the new address once the container is up
			// again; the first start is handled by startContainer.
			go r.refreshWhenRunning(ctx, name, time.Now())
		}
		code, err := r.Engine.AttachBackground(ctx, name, w, w)
		if err != nil {
			fmt.Fprintf(r.Console.Err, "%s: %v\n", name, err)
			code = 1
		}
		// A final line without a newline would otherwise stay buffered.
		if p, ok := w.(interface{ Flush() }); ok {
			p.Flush()
		}
		r.recordExit(name, code)
		select {
		case tracked <- code:
		default:
		}
		if ctx.Err() != nil || !shouldRestart(s.Restart, code) || o.AbortOnExit {
			if ctx.Err() == nil {
				r.Console.Info("%s exited with code %d", name, code)
			}
			done <- code
			return
		}
		r.Console.Info("%s exited with code %d, restarting (%s)", name, code, s.Restart)
		select {
		case <-ctx.Done():
			done <- code
			return
		case <-time.After(time.Second):
		}
	}
}

// refreshWhenRunning waits for a container (re)started after `since` to
// report an address, then rewrites the project's hosts files.
func (r *Runner) refreshWhenRunning(ctx context.Context, name string, since time.Time) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		c, err := r.Engine.InspectContainer(ctx, name)
		if err != nil {
			return
		}
		if c.Running() && c.PrimaryIP() != "" && !c.Status.StartedDate.Before(since.Add(-time.Second)) {
			_ = r.RefreshHosts(ctx)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// followExisting streams logs from a container that was already running.
func (r *Runner) followExisting(ctx context.Context, s types.ServiceConfig, c *engine.Container, logs *ui.PrefixSet, o UpOptions) *startRecord {
	rec := &startRecord{service: s, container: c, started: c.Status.StartedDate, exit: make(chan int, 1)}
	w := logs.Writer(s.Name)
	go func() {
		code := r.followLogs(ctx, c.ID, w)
		done := code
		if ctx.Err() == nil {
			r.Console.Info("%s exited", c.ID)
		}
		rec.exit <- done
	}()
	return rec
}

// followLogs runs `container logs --follow` until the container stops, then
// returns an unknown exit code of 0.
func (r *Runner) followLogs(ctx context.Context, id string, w io.Writer) int {
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := r.Engine.LogsCommand(lctx, id, true, -1)
	cmd.Stdout = w
	cmd.Stderr = w
	engine.Detach(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(r.Console.Err, "%s: cannot follow logs: %v\n", id, err)
		return 0
	}
	go func() {
		for {
			select {
			case <-lctx.Done():
				return
			case <-time.After(time.Second):
			}
			c, err := r.Engine.InspectContainer(lctx, id)
			if err != nil || !c.Running() {
				cancel()
				return
			}
		}
	}()
	_ = cmd.Wait()
	if p, ok := w.(interface{ Flush() }); ok {
		p.Flush()
	}
	return 0
}

// shouldRestart applies compose restart semantics to an exit code.
func shouldRestart(policy string, code int) bool {
	switch {
	case policy == "always" || policy == "unless-stopped":
		return true
	case policy == "on-failure" || len(policy) > len("on-failure") && policy[:len("on-failure")] == "on-failure":
		return code != 0
	}
	return false
}

// recordExit stores an exit code so `wait` and completed_successfully checks
// can see it later; the runtime itself does not retain exit codes.
func (r *Runner) recordExit(name string, code int) {
	dir, err := state.ProjectDir(r.Project.Name)
	if err != nil {
		return
	}
	exits := filepath.Join(dir, "exit")
	_ = os.MkdirAll(exits, 0o755)
	_ = os.WriteFile(filepath.Join(exits, name), []byte(strconv.Itoa(code)), 0o644)
}

// recordedExit returns a stored exit code, if any.
func (r *Runner) recordedExit(name string) (int, bool) {
	dir, err := state.ProjectDir(r.Project.Name)
	if err != nil {
		return 0, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "exit", name))
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(string(b))
	return code, err == nil
}

// waitDependencies enforces depends_on conditions before a service starts.
func (r *Runner) waitDependencies(ctx context.Context, s types.ServiceConfig, scale map[string]int) error {
	if r.Engine.DryRun {
		return nil
	}
	for _, dep := range slices.Sorted(maps.Keys(s.DependsOn)) {
		d := s.DependsOn[dep]
		ds, err := r.Project.GetService(dep)
		if err != nil {
			if !d.Required {
				continue
			}
			return fmt.Errorf("service %s depends on undefined service %s", s.Name, dep)
		}
		cs, err := r.containers(ctx, true)
		if err != nil {
			return err
		}
		cs = serviceContainers(cs, []string{dep}, false)
		// Only the replicas this project expects count; stale extras
		// left by an interrupted run are cleaned up when their service
		// is visited.
		replicas := ds.GetScale()
		if n, ok := scale[dep]; ok {
			replicas = n
		}
		var expected []engine.Container
		for _, c := range cs {
			if n := containerNumber(c); n >= 1 && n <= replicas {
				expected = append(expected, c)
			}
		}
		cs = expected
		if len(cs) == 0 {
			if d.Required {
				return fmt.Errorf("dependency %s of %s has no containers", dep, s.Name)
			}
			continue
		}
		for i := range cs {
			c := &cs[i]
			switch d.Condition {
			case "", types.ServiceConditionStarted:
			case types.ServiceConditionHealthy:
				spec := healthcheckFor(ds)
				if spec == nil {
					return fmt.Errorf("service %s depends on %s being healthy, but %s has no healthcheck", s.Name, dep, dep)
				}
				if !c.Running() {
					return fmt.Errorf("dependency %s of %s is not running", c.ID, s.Name)
				}
				r.Console.Info(" %s Waiting for %s to be healthy", r.Console.Paint("36", "⠿"), c.ID)
				if err := r.waitHealthy(ctx, c, spec, c.Status.StartedDate); err != nil {
					return fmt.Errorf("dependency failed to start: %w", err)
				}
				r.stepOnce("healthy:"+c.ID, "Container", c.ID, "Healthy")
			case types.ServiceConditionCompletedSuccessfully:
				if err := r.waitCompleted(ctx, c); err != nil {
					return err
				}
				r.Console.Step("Container", c.ID, "Exited (0)")
			default:
				return fmt.Errorf("service %s: unknown depends_on condition %q", s.Name, d.Condition)
			}
		}
	}
	return nil
}

// exitCodeNeeded reports whether another service waits for this one to
// complete successfully.
func (r *Runner) exitCodeNeeded(service string) bool {
	for _, s := range r.Project.Services {
		if d, ok := s.DependsOn[service]; ok && d.Condition == types.ServiceConditionCompletedSuccessfully {
			return true
		}
	}
	return false
}

// waitCompleted blocks until the container has stopped with exit code zero.
func (r *Runner) waitCompleted(ctx context.Context, c *engine.Container) error {
	r.Console.Info(" %s Waiting for %s to complete", r.Console.Paint("36", "⠿"), c.ID)
	if ch, ok := r.trackedExit(c.ID); ok {
		select {
		case code := <-ch:
			ch <- code
			if code != 0 {
				return fmt.Errorf("dependency failed to start: container %s exited (%d)", c.ID, code)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		cur, err := r.Engine.InspectContainer(ctx, c.ID)
		if err != nil {
			return err
		}
		if !cur.Running() {
			code, ok := r.recordedExit(c.ID)
			if !ok {
				r.warnOnce("exit:"+c.ID, "exit status of %s is unknown because it was not started by this process; assuming success", c.ID)
				return nil
			}
			if code != 0 {
				return fmt.Errorf("dependency failed to start: container %s exited (%d)", c.ID, code)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// handleOrphans removes or reports containers whose service left the project.
func (r *Runner) handleOrphans(ctx context.Context, existing []engine.Container, remove bool, timeout time.Duration) error {
	known := map[string]bool{}
	for _, n := range r.Project.ServiceNames() {
		known[n] = true
	}
	for _, n := range r.Project.DisabledServiceNames() {
		known[n] = true
	}
	var orphans []engine.Container
	for _, c := range existing {
		if c.Label(project.LabelOneOff) == "True" {
			continue
		}
		if !known[c.Label(project.LabelService)] {
			orphans = append(orphans, c)
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	if !remove {
		names := ids(orphans)
		r.Console.Warn("found orphan containers (%v) for this project; remove them with --remove-orphans", names)
		return nil
	}
	if err := r.stopContainers(ctx, orphans, timeout); err != nil {
		return err
	}
	return r.removeContainers(ctx, orphans, true)
}

// waitAllHealthy implements --wait.
func (r *Runner) waitAllHealthy(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cs, err := r.containers(wctx, true)
	if err != nil {
		return err
	}
	for i := range cs {
		c := &cs[i]
		if c.Label(project.LabelOneOff) == "True" {
			continue
		}
		s, err := r.Project.GetService(c.Label(project.LabelService))
		if err != nil {
			continue
		}
		if !c.Running() {
			return fmt.Errorf("container %s is not running", c.ID)
		}
		if spec := healthcheckFor(s); spec != nil {
			if err := r.waitHealthy(wctx, c, spec, c.Status.StartedDate); err != nil {
				return err
			}
			r.stepOnce("healthy:"+c.ID, "Container", c.ID, "Healthy")
		}
	}
	return nil
}

// attachNames returns the services whose output is shown in the foreground.
func (r *Runner) attachNames(o UpOptions) []string {
	var names []string
	skip := map[string]bool{}
	for _, n := range o.NoAttach {
		skip[n] = true
	}
	only := map[string]bool{}
	for _, n := range o.Attach {
		only[n] = true
	}
	if len(only) == 0 && !o.AttachDependencies {
		for _, n := range o.Services {
			only[n] = true
		}
	}
	for _, n := range serviceOrder(r.Project, r.Project.ServiceNames()) {
		if skip[n] {
			continue
		}
		if len(only) > 0 && !only[n] {
			continue
		}
		names = append(names, n)
	}
	return names
}

// attachPhase keeps the foreground session alive until the containers exit or
// the user interrupts, then stops the project.
func (r *Runner) attachPhase(ctx context.Context, o UpOptions, records []*startRecord, logs *ui.PrefixSet) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	type exited struct {
		rec  *startRecord
		code int
	}
	exits := make(chan exited, len(records))
	for _, rec := range records {
		go func(rec *startRecord) {
			code := <-rec.exit
			exits <- exited{rec, code}
		}(rec)
	}
	remaining := len(records)
	exitCode := 0
	codes := map[string]int{}
	abort := false
	for remaining > 0 && !abort {
		select {
		case e := <-exits:
			remaining--
			codes[e.rec.service.Name] = e.code
			if o.AbortOnExit {
				abort = true
			}
		case <-sigs:
			r.Console.Info("Gracefully stopping... (press Ctrl+C again to force)")
			abort = true
			go func() {
				<-sigs
				cs, _ := r.containers(context.Background(), false)
				_ = r.Engine.Kill(context.Background(), ids(cs), "")
			}()
		case <-ctx.Done():
			abort = true
		}
	}
	if abort {
		stopCtx := context.Background()
		cs, err := r.containers(stopCtx, false)
		if err == nil {
			_ = r.stopContainers(stopCtx, orderContainers(r.Project, cs, true), o.Timeout)
		}
		// Drain attached processes now that their containers are gone.
		deadline := time.After(30 * time.Second)
		for remaining > 0 {
			select {
			case e := <-exits:
				remaining--
				codes[e.rec.service.Name] = e.code
			case <-deadline:
				remaining = 0
			}
		}
	}
	if o.ExitCodeFrom != "" {
		if code, ok := codes[o.ExitCodeFrom]; ok {
			exitCode = code
		}
	}
	return exitCode, nil
}

var errInterrupted = errors.New("interrupted")

// NoStartDeps reports whether dependencies should be left out.
func (o UpOptions) NoStartDeps() bool { return o.NoDeps }
