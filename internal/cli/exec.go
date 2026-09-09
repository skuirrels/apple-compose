package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) execCommand() *cobra.Command {
	var o compose.ExecOptions
	var noTTY, privileged bool
	var interactive bool
	cmd := &cobra.Command{
		Use:   "exec [OPTIONS] SERVICE COMMAND [ARGS...]",
		Short: "Execute a command in a running container",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			o.Service = args[0]
			o.Command = args[1:]
			o.NoTTY = noTTY
			o.Interactive = interactive
			if privileged {
				a.console.Warn("--privileged is ignored by the container runtime")
			}
			code, err := a.runner(p).Exec(cmd.Context(), o)
			if err != nil {
				return err
			}
			return exit(code)
		},
	}
	f := cmd.Flags()
	f.SetInterspersed(false)
	f.BoolVarP(&o.Detach, "detach", "d", false, "Detached mode: Run command in the background")
	f.StringArrayVarP(&o.Env, "env", "e", nil, "Set environment variables")
	f.IntVar(&o.Index, "index", 0, "Index of the container if service has multiple replicas")
	f.BoolVarP(&interactive, "interactive", "i", true, "Keep STDIN open even if not attached")
	f.BoolVar(&privileged, "privileged", false, "Give extended privileges to the process (ignored)")
	f.BoolVarP(&noTTY, "no-TTY", "T", false, "Disable pseudo-TTY allocation. By default `exec` allocates a TTY")
	f.StringVarP(&o.User, "user", "u", "", "Run the command as this user")
	f.StringVarP(&o.WorkDir, "workdir", "w", "", "Path to workdir directory for this command")
	return cmd
}

func (a *App) runCommand() *cobra.Command {
	var o compose.RunOptions
	var entrypoint string
	var labels []string
	var pull string
	var noTTY, interactive bool
	cmd := &cobra.Command{
		Use:   "run [OPTIONS] SERVICE [COMMAND] [ARGS...]",
		Short: "Run a one-off command on a service",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), []string{args[0]}, !o.NoDeps)
			if err != nil {
				return err
			}
			o.Service = args[0]
			o.Command = args[1:]
			o.NoTTY = noTTY
			o.Interactive = interactive
			o.Pull = compose.PullPolicy(pull)
			if cmd.Flags().Changed("entrypoint") {
				o.Entrypoint = &entrypoint
			}
			o.Labels = map[string]string{}
			for _, l := range labels {
				k, v, _ := strings.Cut(l, "=")
				o.Labels[k] = v
			}
			code, err := a.runner(p).Run(cmd.Context(), o)
			if err != nil {
				return err
			}
			return exit(code)
		},
	}
	f := cmd.Flags()
	f.SetInterspersed(false)
	f.BoolVar(&o.Build, "build", false, "Build image before starting container")
	f.BoolVarP(&o.Detach, "detach", "d", false, "Run container in background and print container ID")
	f.StringVar(&entrypoint, "entrypoint", "", "Override the entrypoint of the image")
	f.StringArrayVarP(&o.Env, "env", "e", nil, "Set environment variables")
	f.BoolVarP(&interactive, "interactive", "i", true, "Keep STDIN open even if not attached")
	f.StringArrayVarP(&labels, "label", "l", nil, "Add or override a label")
	f.StringVar(&o.Name, "name", "", "Assign a name to the container")
	f.BoolVarP(&noTTY, "no-TTY", "T", false, "Disable pseudo-TTY allocation (default: auto-detected)")
	f.BoolVar(&o.NoDeps, "no-deps", false, "Don't start linked services")
	f.StringArrayVarP(&o.Publish, "publish", "p", nil, "Publish a container's port(s) to the host")
	f.StringVar(&pull, "pull", "", `Pull image before running ("always"|"missing"|"never")`)
	f.BoolVar(&o.QuietPull, "quiet-pull", false, "Pull without printing progress information")
	f.BoolVar(&o.RemoveOrphans, "remove-orphans", false, "Remove containers for services not defined in the Compose file")
	f.BoolVar(&o.Remove, "rm", false, "Automatically remove the container when it exits")
	f.BoolVar(&o.ServicePorts, "service-ports", false, "Run command with all service's ports enabled and mapped to the host")
	f.BoolVar(&o.UseAliasesFlag, "use-aliases", false, "Accepted for compatibility: aliases are always applied")
	_ = f.MarkHidden("use-aliases")
	f.StringVarP(&o.User, "user", "u", "", "Run as specified username or uid")
	f.StringArrayVarP(&o.Volumes, "volume", "v", nil, "Bind mount a volume")
	f.StringVarP(&o.WorkDir, "workdir", "w", "", "Working directory inside the container")
	cmd.Example = "  apple-compose run --rm web sh\n  apple-compose run -d worker"
	return cmd
}
