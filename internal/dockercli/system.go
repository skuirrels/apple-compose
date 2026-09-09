package dockercli

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/cli"
)

func (a *App) versionCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "version [OPTIONS]",
		Short: "Show the apple-docker and container runtime version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			server := "unavailable"
			if a.eng != nil {
				if v, err := a.eng.Version(cmd.Context()); err == nil {
					server = v
				}
			}
			info := map[string]any{
				"Client": map[string]string{"Version": a.version, "Os": runtime.GOOS, "Arch": runtime.GOARCH, "GoVersion": runtime.Version()},
				"Server": map[string]string{"Version": server, "Runtime": "apple/container"},
			}
			if format != "" {
				return renderInspect(a.console.Out, format, []any{info})
			}
			fmt.Fprintf(a.console.Out, "Client:\n Version:    %s\n OS/Arch:    %s/%s\n Go version: %s\n\nServer: Apple container runtime\n Version:    %s\n", a.version, runtime.GOOS, runtime.GOARCH, runtime.Version(), server)
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "", "Format output using a custom template")
	return cmd
}

func (a *App) infoCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "info [OPTIONS]",
		Short: "Display system-wide information",
		RunE: func(cmd *cobra.Command, args []string) error {
			cs, err := a.eng.ListContainers(cmd.Context(), true)
			if err != nil {
				return err
			}
			running := 0
			for _, c := range cs {
				if c.Running() {
					running++
				}
			}
			imgs, _ := a.eng.ListImages(cmd.Context())
			server, _ := a.eng.Version(cmd.Context())
			info := map[string]any{
				"ID":                "apple-container",
				"Containers":        len(cs),
				"ContainersRunning": running,
				"ContainersStopped": len(cs) - running,
				"Images":            len(imgs),
				"ServerVersion":     server,
				"Driver":            "apple-container",
				"OperatingSystem":   "macOS",
				"OSType":            "linux",
				"Architecture":      runtime.GOARCH,
				"Name":              hostname(),
				"ClientVersion":     a.version,
			}
			if format != "" {
				return renderInspect(a.console.Out, format, []any{info})
			}
			fmt.Fprintf(a.console.Out, "Client:\n Version:    %s\n\nServer:\n Containers: %d\n  Running: %d\n  Stopped: %d\n Images: %d\n Server Version: %s\n Runtime: Apple container (one virtual machine per container)\n Operating System: macOS\n OSType: linux\n Architecture: %s\n Name: %s\n",
				a.version, len(cs), running, len(cs)-running, len(imgs), server, runtime.GOARCH, hostname())
			return nil
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "", "Format output using a custom template")
	return cmd
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func (a *App) systemCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "system", Short: "Manage the container runtime"}

	var dfFormat string
	df := &cobra.Command{
		Use:   "df [OPTIONS]",
		Short: "Show disk usage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dfFormat == "json" {
				return a.exec("system", "df", "--format", "json")
			}
			return a.exec("system", "df")
		},
	}
	df.Flags().StringVar(&dfFormat, "format", "", "Format output ('json' or the runtime's table)")
	df.Flags().BoolP("verbose", "v", false, "Accepted for compatibility with Docker; ignored")
	_ = df.Flags().MarkHidden("verbose")

	var all, force, volumes bool
	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused data",
		RunE: func(cmd *cobra.Command, args []string) error {
			msg := "WARNING! This will remove:\n  - all stopped containers\n  - all networks not used by at least one container\n  - all dangling images"
			if all {
				msg += "\n  - all images without at least one container associated to them"
			}
			if volumes {
				msg += "\n  - all volumes not used by at least one container"
			}
			if !force && !a.confirm(msg+"\n\nAre you sure you want to continue?") {
				return nil
			}
			steps := [][]string{{"prune"}, {"image", "prune"}}
			if all {
				steps[1] = append(steps[1], "--all")
			}
			if volumes {
				steps = append(steps, []string{"volume", "prune"})
			}
			for _, s := range steps {
				if err := a.runAttached(cmd.Context(), s...); err != nil {
					return err
				}
			}
			return a.pruneNetworks(cmd.Context())
		},
	}
	prune.Flags().BoolVarP(&all, "all", "a", false, "Remove all unused images not just dangling ones")
	prune.Flags().BoolVarP(&force, "force", "f", false, "Do not prompt for confirmation")
	prune.Flags().BoolVar(&volumes, "volumes", false, "Prune anonymous volumes")

	events := a.unsupported("events", "Get real time events from the server", "the container runtime has no event stream")
	cmd.AddCommand(df, prune, events, a.infoCommand(), a.versionCommand(), a.cleanCommand())
	return cmd
}

func (a *App) loginCommand() *cobra.Command {
	var username, password string
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "login [OPTIONS] [SERVER]",
		Short: "Authenticate to a registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server := "docker.io"
			if len(args) == 1 {
				server = args[0]
			}
			largs := []string{"registry", "login"}
			if username != "" {
				largs = append(largs, "--username", username)
			}
			if password != "" {
				// The runtime only takes passwords on stdin; feed it ours.
				a.warn("--password is insecure; prefer --password-stdin")
				a.in = strings.NewReader(password + "\n")
				passwordStdin = true
			}
			if passwordStdin {
				largs = append(largs, "--password-stdin")
			}
			return a.runAttached(cmd.Context(), append(largs, server)...)
		},
	}
	cmd.Flags().StringVarP(&username, "username", "u", "", "Username")
	cmd.Flags().StringVarP(&password, "password", "p", "", "Password")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "Take the password from stdin")
	return cmd
}

func (a *App) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [SERVER]",
		Short: "Log out from a registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server := "docker.io"
			if len(args) == 1 {
				server = args[0]
			}
			return a.runAttached(cmd.Context(), "registry", "logout", server)
		},
	}
}

// composeCommand hands `docker compose ...` to apple-compose in-process.
func (a *App) composeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "compose [OPTIONS] COMMAND",
		Short:              "Define and run multi-container applications (apple-compose)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exit(cli.Execute(a.version, args))
		},
	}
	return cmd
}
