package compose

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
)

// WatchOptions configure Watch.
type WatchOptions struct {
	// Services limits the watch to these services; empty means every
	// service that declares develop.watch.
	Services []string
	// NoUp leaves the services alone instead of starting them first.
	NoUp bool
	// Interval is how often the watched trees are rescanned.
	Interval time.Duration
	// Prune deletes files inside the container when they disappear from
	// the host.
	Prune bool
}

// DefaultWatchInterval is how often Watch looks for changes. Compose watches
// source trees, where a fraction of a second is imperceptible and a full
// rescan is cheap.
const DefaultWatchInterval = 500 * time.Millisecond

// watchRule is one resolved develop.watch trigger.
type watchRule struct {
	service string
	root    string // absolute host path
	action  types.WatchAction
	target  string
	include []string
	ignore  []string
	exec    types.ServiceHook
	initial bool
	// file is set when the rule watches a single file rather than a tree.
	file bool
}

// stamp identifies a file version cheaply enough to poll for.
type stamp struct {
	mod  time.Time
	size int64
}

// Watch keeps containers in step with the source tree, applying each
// develop.watch trigger as files change. It returns when the context ends,
// which is what Ctrl-C does; the containers keep running, as Compose leaves
// them.
func (r *Runner) Watch(ctx context.Context, o WatchOptions) error {
	rules, err := r.watchRules(o.Services)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return errors.New("no service declares develop.watch; add one to the compose file")
	}
	if !o.NoUp {
		services := o.Services
		if len(services) == 0 {
			services = watchServices(rules)
		}
		if _, err := r.Up(ctx, UpOptions{Detach: true, Services: services, Parallelism: 4}); err != nil {
			return err
		}
	}
	if o.Interval <= 0 {
		o.Interval = DefaultWatchInterval
	}
	state := make([]map[string]stamp, len(rules))
	for i, rule := range rules {
		state[i] = scanTree(rule)
		if rule.initial && rule.action != types.WatchActionRebuild {
			if err := r.applyWatch(ctx, rule, sortedPaths(state[i]), nil, o); err != nil {
				r.Console.Warn("watch %s: %v", rule.service, err)
			}
		}
	}
	for _, name := range watchServices(rules) {
		r.Console.Step("Watch", name, "watching")
	}
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		for i, rule := range rules {
			now := scanTree(rule)
			changed, deleted := diffTrees(state[i], now)
			state[i] = now
			if len(changed) == 0 && len(deleted) == 0 {
				continue
			}
			if err := r.applyWatch(ctx, rule, changed, deleted, o); err != nil {
				r.Console.Fail("Watch", rule.service, string(rule.action), err)
			}
		}
	}
}

// watchServices lists the services covered by a set of rules, in order.
func watchServices(rules []watchRule) []string {
	var out []string
	for _, rule := range rules {
		if !slices.Contains(out, rule.service) {
			out = append(out, rule.service)
		}
	}
	return out
}

// watchRules resolves the develop.watch triggers of the selected services.
func (r *Runner) watchRules(services []string) ([]watchRule, error) {
	var out []watchRule
	for _, name := range slices.Sorted(maps.Keys(r.Project.Services)) {
		s := r.Project.Services[name]
		if len(services) > 0 && !slices.Contains(services, name) {
			continue
		}
		if s.Develop == nil {
			continue
		}
		for _, t := range s.Develop.Watch {
			root := t.Path
			if !filepath.IsAbs(root) {
				root = filepath.Join(r.Project.WorkingDir, root)
			}
			root = filepath.Clean(root)
			info, err := os.Stat(root)
			if err != nil {
				return nil, fmt.Errorf("service %s watches %s: %w", name, t.Path, err)
			}
			switch t.Action {
			case types.WatchActionSync, types.WatchActionSyncRestart, types.WatchActionSyncExec:
				if t.Target == "" {
					return nil, fmt.Errorf("service %s: a %s trigger needs a target", name, t.Action)
				}
			case types.WatchActionRebuild, types.WatchActionRestart:
			default:
				return nil, fmt.Errorf("service %s: unknown watch action %q", name, t.Action)
			}
			out = append(out, watchRule{
				service: name, root: root, action: t.Action, target: t.Target,
				include: t.Include, ignore: t.Ignore, exec: t.Exec,
				initial: t.InitialSync, file: !info.IsDir(),
			})
		}
	}
	return out, nil
}

// scanTree records the current state of a watched path. Errors are folded
// into an empty result: a tree that cannot be read this time round is simply
// reported as changed when it can be.
func scanTree(rule watchRule) map[string]stamp {
	out := map[string]stamp{}
	if rule.file {
		if info, err := os.Stat(rule.root); err == nil {
			out[rule.root] = stamp{info.ModTime(), info.Size()}
		}
		return out
	}
	_ = filepath.WalkDir(rule.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(rule.root, p)
		if relErr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if watchIgnores(rule, rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if watchIgnores(rule, rel, false) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out[p] = stamp{info.ModTime(), info.Size()}
		return nil
	})
	return out
}

// watchIgnores applies a trigger's include and ignore patterns to one path
// relative to the watched root. Patterns match a whole relative path when
// they contain a separator, and any single path segment otherwise, which is
// how Compose treats entries such as "node_modules".
func watchIgnores(rule watchRule, rel string, dir bool) bool {
	segments := strings.Split(filepath.ToSlash(rel), "/")
	if slices.Contains(segments, ".git") {
		return true
	}
	for _, pattern := range rule.ignore {
		if matchWatchPattern(pattern, rel, segments) {
			return true
		}
	}
	// A directory is kept whatever the include patterns say, because the
	// files under it may well match.
	if len(rule.include) == 0 || dir {
		return false
	}
	for _, pattern := range rule.include {
		if matchWatchPattern(pattern, rel, segments) {
			return false
		}
	}
	return true
}

func matchWatchPattern(pattern, rel string, segments []string) bool {
	pattern = strings.TrimSuffix(filepath.ToSlash(pattern), "/")
	if pattern == "" {
		return false
	}
	slashed := filepath.ToSlash(rel)
	if strings.Contains(pattern, "/") {
		if ok, _ := path.Match(pattern, slashed); ok {
			return true
		}
		// A directory pattern covers everything beneath it.
		return strings.HasPrefix(slashed, pattern+"/")
	}
	for _, seg := range segments {
		if ok, _ := path.Match(pattern, seg); ok {
			return true
		}
	}
	return false
}

// diffTrees reports the paths that appeared or changed and those that went.
func diffTrees(before, after map[string]stamp) (changed, deleted []string) {
	for p, now := range after {
		was, ok := before[p]
		if !ok || was != now {
			changed = append(changed, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted
}

func sortedPaths(m map[string]stamp) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// applyWatch carries one batch of changes into the containers.
func (r *Runner) applyWatch(ctx context.Context, rule watchRule, changed, deleted []string, o WatchOptions) error {
	switch rule.action {
	case types.WatchActionRebuild:
		r.Console.Step("Watch", rule.service, "rebuilding")
		_, err := r.Up(ctx, UpOptions{
			Detach: true, Build: true, ForceRecreate: true, NoDeps: true,
			Services: []string{rule.service}, Parallelism: 1,
		})
		return err
	case types.WatchActionRestart:
		r.Console.Step("Watch", rule.service, "restarting")
		return r.Restart(ctx, []string{rule.service}, 0, true)
	}
	cs, err := r.serviceRunning(ctx, rule.service)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if err := r.syncTo(ctx, c, rule, changed, deleted, o.Prune); err != nil {
			return err
		}
	}
	switch rule.action {
	case types.WatchActionSyncRestart:
		r.Console.Step("Watch", rule.service, "restarting")
		return r.Restart(ctx, []string{rule.service}, 0, true)
	case types.WatchActionSyncExec:
		return r.runWatchHook(ctx, rule, cs)
	}
	return nil
}

// serviceRunning returns the running containers of one service.
func (r *Runner) serviceRunning(ctx context.Context, service string) ([]engine.Container, error) {
	cs, err := r.containers(ctx, false)
	if err != nil {
		return nil, err
	}
	var out []engine.Container
	for _, c := range cs {
		if c.Label(project.LabelService) == service && c.Running() {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("service %s has no running container to sync into", service)
	}
	return out, nil
}

// syncTo copies changed files into one container and, when asked, removes
// the ones that have gone.
func (r *Runner) syncTo(ctx context.Context, c engine.Container, rule watchRule, changed, deleted []string, prune bool) error {
	made := map[string]bool{}
	for _, p := range changed {
		target, err := watchTarget(rule, p)
		if err != nil {
			return err
		}
		if dir := path.Dir(target); dir != "." && dir != "/" && !made[dir] {
			made[dir] = true
			r.mkdirIn(ctx, c.ID, dir)
		}
		if err := r.Engine.Copy(ctx, p, c.ID+":"+target); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(p), err)
		}
		r.Console.Step("Watch", rule.service, "synced "+watchRel(rule, p))
	}
	if !prune {
		return nil
	}
	for _, p := range deleted {
		target, err := watchTarget(rule, p)
		if err != nil {
			return err
		}
		if _, err := r.Engine.Exec(ctx, c.ID, engine.ExecOptions{User: "0:0"}, []string{"rm", "-rf", target}, nil, nil, nil); err != nil {
			r.Console.Warn("watch %s: remove %s: %v", rule.service, target, err)
			continue
		}
		r.Console.Step("Watch", rule.service, "removed "+watchRel(rule, p))
	}
	return nil
}

// mkdirIn creates a directory inside a container, tolerating images without
// a shell: the sync itself still works when the directory already exists.
func (r *Runner) mkdirIn(ctx context.Context, id, dir string) {
	if _, err := r.Engine.Exec(ctx, id, engine.ExecOptions{User: "0:0"}, []string{"mkdir", "-p", dir}, nil, nil, nil); err != nil {
		r.warnOnce("watch-mkdir-"+id, "watch: could not create %s in %s (%v); syncs into new directories may fail", dir, id, err)
	}
}

// watchTarget maps a host path to its place inside the container.
func watchTarget(rule watchRule, host string) (string, error) {
	if rule.file {
		return rule.target, nil
	}
	rel, err := filepath.Rel(rule.root, host)
	if err != nil {
		return "", err
	}
	return path.Join(rule.target, filepath.ToSlash(rel)), nil
}

// watchRel names a changed file the way the user wrote the trigger.
func watchRel(rule watchRule, host string) string {
	if rel, err := filepath.Rel(rule.root, host); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(host)
}

// runWatchHook runs a sync+exec trigger's command in the service containers.
func (r *Runner) runWatchHook(ctx context.Context, rule watchRule, cs []engine.Container) error {
	if len(rule.exec.Command) == 0 {
		return nil
	}
	r.Console.Step("Watch", rule.service, "exec "+strings.Join(rule.exec.Command, " "))
	opts := engine.ExecOptions{User: rule.exec.User, WorkDir: rule.exec.WorkingDir}
	for _, k := range slices.Sorted(maps.Keys(rule.exec.Environment)) {
		if v := rule.exec.Environment[k]; v != nil {
			opts.Env = append(opts.Env, k+"="+*v)
		}
	}
	targets := cs
	if !rule.exec.PerReplica {
		targets = cs[:1]
	}
	for _, c := range targets {
		code, err := r.Engine.Exec(ctx, c.ID, opts, rule.exec.Command, nil, r.Console.Out, r.Console.Err)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("exec exited with %d", code)
		}
	}
	return nil
}
