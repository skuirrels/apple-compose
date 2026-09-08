package compose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// ExecOptions configure Exec.
type ExecOptions struct {
	Service     string
	Index       int
	Detach      bool
	User        string
	WorkDir     string
	Env         []string
	NoTTY       bool
	Interactive bool
	Command     []string
}

// Exec runs a command inside a running service container.
func (r *Runner) Exec(ctx context.Context, o ExecOptions) (int, error) {
	c, err := r.findContainer(ctx, o.Service, o.Index, false)
	if err != nil {
		return 1, err
	}
	if !c.Running() {
		return 1, fmt.Errorf("container %s is not running", c.ID)
	}
	tty := !o.NoTTY && ui.IsTerminal(os.Stdin) && ui.IsTerminal(os.Stdout)
	opts := engine.ExecOptions{
		Interactive: o.Interactive && !o.Detach,
		TTY:         tty && !o.Detach,
		Detach:      o.Detach,
		User:        o.User,
		WorkDir:     o.WorkDir,
		Env:         o.Env,
	}
	return r.Engine.Exec(ctx, c.ID, opts, o.Command, os.Stdin, os.Stdout, os.Stderr)
}

// RunOptions configure Run.
type RunOptions struct {
	Service        string
	Command        []string
	Detach         bool
	Remove         bool
	Name           string
	Entrypoint     *string
	Env            []string
	User           string
	WorkDir        string
	Volumes        []string
	Publish        []string
	ServicePorts   bool
	NoDeps         bool
	NoTTY          bool
	Interactive    bool
	Labels         map[string]string
	QuietPull      bool
	Build          bool
	RemoveOrphans  bool
	Pull           PullPolicy
	UseAliasesFlag bool
}

// Run starts a one-off container for a service, like `docker compose run`.
func (r *Runner) Run(ctx context.Context, o RunOptions) (int, error) {
	s, err := r.Project.GetService(o.Service)
	if err != nil {
		return 1, err
	}
	if err := r.Engine.CheckRunning(ctx); err != nil {
		return 1, err
	}
	if err := r.EnsureNetworks(ctx); err != nil {
		return 1, err
	}
	if err := r.EnsureVolumes(ctx); err != nil {
		return 1, err
	}
	if !o.NoDeps && len(s.GetDependencies()) > 0 {
		deps, err := r.Project.WithSelectedServices(s.GetDependencies())
		if err != nil {
			return 1, err
		}
		sub := New(r.Engine, deps, r.Console, r.Version)
		if _, err := sub.Up(ctx, UpOptions{Detach: true, QuietPull: o.QuietPull, Build: o.Build, Pull: o.Pull, RemoveOrphans: o.RemoveOrphans}); err != nil {
			return 1, err
		}
	}
	if err := r.EnsureImage(ctx, s, ImageOptions{Build: o.Build, Pull: o.Pull, Quiet: o.QuietPull}); err != nil {
		return 1, err
	}
	name := o.Name
	if name == "" {
		name = project.OneOffName(r.Project, s, slug())
	}
	hostsPath, err := r.hostsPathFor(name)
	if err != nil {
		return 1, err
	}
	tty := !o.NoTTY && ui.IsTerminal(os.Stdin) && !o.Detach
	spec := createSpec{
		service:    s,
		name:       name,
		number:     1,
		oneOff:     true,
		hostsPath:  hostsPath,
		command:    o.Command,
		env:        o.Env,
		user:       o.User,
		workdir:    o.WorkDir,
		volumes:    o.Volumes,
		ports:      o.Publish,
		labels:     o.Labels,
		noPorts:    !o.ServicePorts,
		tty:        tty,
		stdin:      o.Interactive && !o.Detach,
		autoRemove: o.Remove && o.Detach,
	}
	if len(o.Command) == 0 {
		spec.command = nil
	}
	if o.Entrypoint != nil {
		ep := []string{}
		if *o.Entrypoint != "" {
			ep = []string{*o.Entrypoint}
		}
		spec.entrypoint = &ep
	}
	hash, _ := ServiceHash(s)
	spec.hash = hash
	args, err := r.createArgs(spec)
	if err != nil {
		return 1, err
	}
	if _, err := r.Engine.Create(ctx, args...); err != nil {
		return 1, err
	}
	if o.Detach {
		if err := r.Engine.Start(ctx, name); err != nil {
			return 1, err
		}
		if _, err := r.waitRunning(ctx, name, 2*time.Minute); err == nil {
			_ = r.RefreshHosts(ctx)
		}
		fmt.Fprintln(r.Console.Out, name)
		return 0, nil
	}
	go func() {
		if _, err := r.waitRunning(ctx, name, 2*time.Minute); err == nil {
			_ = r.RefreshHosts(ctx)
		}
	}()
	var stdin *os.File
	if o.Interactive {
		stdin = os.Stdin
	}
	code, err := r.Engine.Attach(ctx, name, stdin, os.Stdout, os.Stderr, o.Interactive)
	r.recordExit(name, code)
	if o.Remove {
		_ = r.Engine.Delete(context.Background(), []string{name}, true)
	}
	return code, err
}

func slug() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// oneOffService is a helper for building a spec from a service by name.
func (r *Runner) service(name string) (types.ServiceConfig, error) {
	return r.Project.GetService(name)
}
