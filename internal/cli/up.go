package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) upCommand() *cobra.Command {
	var (
		o       compose.UpOptions
		timeout int
		wait    int
		pull    string
		scale   []string
		noColor bool
		watch   bool
	)
	cmd := &cobra.Command{
		Use:   "up [OPTIONS] [SERVICE...]",
		Short: "Create and start containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), args, !o.NoStartDeps())
			if err != nil {
				return err
			}
			o.Services = args
			o.Timeout = time.Duration(timeout) * time.Second
			o.WaitTimeout = time.Duration(wait) * time.Second
			o.Pull = compose.PullPolicy(pull)
			o.Parallelism = a.g.parallel
			o.Scale = map[string]int{}
			for _, s := range scale {
				name, n, ok := strings.Cut(s, "=")
				v, err := strconv.Atoi(n)
				if !ok || err != nil {
					return fmt.Errorf("invalid --scale %q, expected SERVICE=NUM", s)
				}
				o.Scale[name] = v
			}
			if noColor {
				a.console.Colour = false
			}
			if watch {
				// Watching needs the services in the background; Compose
				// keeps them running when the watch stops.
				o.Detach = true
			}
			runner := a.runner(p)
			code, err := runner.Up(cmd.Context(), o)
			if err != nil {
				return err
			}
			if watch && code == 0 {
				return a.watchAfterUp(cmd, runner, args, compose.DefaultWatchInterval)
			}
			return exit(code)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.Detach, "detach", "d", false, "Detached mode: run containers in the background")
	f.BoolVar(&o.Build, "build", false, "Build images before starting containers")
	f.BoolVar(&o.NoBuild, "no-build", false, "Don't build an image, even if it's missing")
	f.BoolVar(&o.ForceRecreate, "force-recreate", false, "Recreate containers even if their configuration and image haven't changed")
	f.BoolVar(&o.NoRecreate, "no-recreate", false, "If containers already exist, don't recreate them")
	f.BoolVar(&o.AlwaysRecreateDeps, "always-recreate-deps", false, "Recreate dependent containers")
	f.BoolVar(&o.NoStart, "no-start", false, "Don't start the services after creating them")
	f.BoolVar(&o.RemoveOrphans, "remove-orphans", false, "Remove containers for services not defined in the Compose file")
	f.StringVar(&pull, "pull", "", `Pull image before running ("always"|"missing"|"never")`)
	f.BoolVar(&o.QuietPull, "quiet-pull", false, "Pull without printing progress information")
	f.BoolVar(&o.Wait, "wait", false, "Wait for services to be running|healthy. Implies detached mode")
	f.IntVar(&wait, "wait-timeout", 0, "Maximum duration in seconds to wait for the project to be running|healthy")
	f.IntVarP(&timeout, "timeout", "t", 0, "Use this timeout in seconds for container shutdown when attached or when containers are already running")
	f.BoolVar(&o.AbortOnExit, "abort-on-container-exit", false, "Stops all containers if any container was stopped. Incompatible with -d")
	f.StringVar(&o.ExitCodeFrom, "exit-code-from", "", "Return the exit code of the selected service container. Implies --abort-on-container-exit")
	f.StringArrayVar(&o.Attach, "attach", nil, "Restrict attaching to the specified services. Incompatible with --attach-dependencies")
	f.StringArrayVar(&o.NoAttach, "no-attach", nil, "Do not attach (stream logs) to the specified services")
	f.BoolVar(&o.AttachDependencies, "attach-dependencies", false, "Automatically attach to log output of dependent services")
	f.BoolVar(&o.NoLogPrefix, "no-log-prefix", false, "Don't print prefix in logs")
	f.BoolVar(&noColor, "no-color", false, "Produce monochrome output")
	f.StringArrayVar(&scale, "scale", nil, "Scale SERVICE to NUM instances. Overrides the `scale` setting in the Compose file if present")
	f.BoolVar(&o.NoDeps, "no-deps", false, "Don't start linked services")
	f.BoolVarP(&watch, "watch", "w", false, "Watch source code and rebuild or refresh containers when files change. Implies detached mode")
	f.Bool("renew-anon-volumes", false, "Accepted for compatibility")
	_ = f.MarkHidden("renew-anon-volumes")
	f.Bool("no-log-color", false, "Accepted for compatibility")
	_ = f.MarkHidden("no-log-color")
	return cmd
}

func (a *App) createCommand() *cobra.Command {
	var o compose.UpOptions
	var pull string
	cmd := &cobra.Command{
		Use:   "create [OPTIONS] [SERVICE...]",
		Short: "Creates containers for a service",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), args, !o.NoDeps)
			if err != nil {
				return err
			}
			o.NoStart = true
			o.Detach = true
			o.Pull = compose.PullPolicy(pull)
			_, err = a.runner(p).Up(cmd.Context(), o)
			return err
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.Build, "build", false, "Build images before starting containers")
	f.BoolVar(&o.NoBuild, "no-build", false, "Don't build an image, even if it's missing")
	f.BoolVar(&o.ForceRecreate, "force-recreate", false, "Recreate containers even if their configuration and image haven't changed")
	f.BoolVar(&o.NoRecreate, "no-recreate", false, "If containers already exist, don't recreate them")
	f.BoolVar(&o.RemoveOrphans, "remove-orphans", false, "Remove containers for services not defined in the Compose file")
	f.StringVar(&pull, "pull", "", `Pull image before running ("always"|"missing"|"never")`)
	f.BoolVar(&o.QuietPull, "quiet-pull", false, "Pull without printing progress information")
	f.BoolVar(&o.NoDeps, "no-deps", false, "Don't create dependent services")
	return cmd
}
