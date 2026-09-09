// Package dockercli is a Docker CLI front end for Apple's container runtime:
// Docker's command names, flags and output shapes, translated onto the
// `container` CLI. Attached commands replace the process with the runtime's
// own so TTYs and signals pass straight through; listing commands read the
// runtime's JSON and render Docker's tables and formats.
package dockercli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// App wires the command tree together.
type App struct {
	version string
	eng     *engine.Engine
	console *ui.Console
	in      io.Reader
	debug   bool
	dryRun  bool
	// execFn replaces the current process with the runtime CLI. Tests
	// substitute a recorder.
	execFn func(bin string, args []string) error
	// spawnFn launches the restart supervisor; tests substitute a recorder.
	spawnFn compose.SupervisorSpawner
	// exited is set when execFn returned instead of replacing the process,
	// so tests can observe the translated command.
	execd []string
}

// ExitCodeError carries a process exit code out of a command.
type ExitCodeError struct{ Code int }

func (e ExitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Execute runs the CLI and returns the process exit code.
func Execute(version string, args []string) int {
	app := &App{version: version, in: os.Stdin}
	root := app.rootCommand()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var ec ExitCodeError
		if errors.As(err, &ec) {
			return ec.Code
		}
		w := io.Writer(os.Stderr)
		if app.console != nil {
			w = app.console.Err
		}
		fmt.Fprintln(w, "Error:", err)
		return 1
	}
	return 0
}

func (a *App) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "apple-docker",
		Short:         "Docker's command line, running on Apple's container runtime",
		Long:          "apple-docker accepts Docker's commands and flags and runs them on Apple's `container` runtime for macOS. Alias it as `docker` to keep existing scripts working.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Docker's global flags precede the subcommand; keeping them off the
		// persistent set avoids shorthand clashes such as -l.
		TraverseChildren: true,
		Version:          a.version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if a.console == nil {
				a.console = ui.NewConsole("auto")
			}
			if a.eng != nil {
				return nil
			}
			eng, err := engine.New()
			if err != nil {
				if !needsRuntime(cmd) {
					return nil
				}
				return err
			}
			eng.Debug = a.debug
			eng.DryRun = a.dryRun
			eng.Log = a.console.Err
			a.eng = eng
			return nil
		},
	}
	root.SetVersionTemplate("apple-docker version {{.Version}}\n")
	pf := root.Flags()
	pf.BoolVarP(&a.debug, "debug", "D", false, "Print every container command as it runs")
	pf.BoolVar(&a.dryRun, "dry-run", false, "Print the container commands that would run without executing them")
	for _, ignored := range []string{"host", "context", "config", "log-level", "tlscacert", "tlscert", "tlskey"} {
		pf.String(ignored, "", "Accepted for compatibility with Docker; ignored")
		_ = pf.MarkHidden(ignored)
	}
	for _, ignored := range []string{"tls", "tlsverify"} {
		pf.Bool(ignored, false, "Accepted for compatibility with Docker; ignored")
		_ = pf.MarkHidden(ignored)
	}
	pf.StringP("host-short", "H", "", "Accepted for compatibility with Docker; ignored")
	_ = pf.MarkHidden("host-short")
	pf.StringP("log-level-short", "l", "", "Accepted for compatibility with Docker; ignored")
	_ = pf.MarkHidden("log-level-short")

	root.AddCommand(
		a.runCommand(), a.createCommand(), a.startCommand(), a.stopCommand(), a.restartCommand(),
		a.killCommand(), a.rmCommand(), a.psCommand(), a.logsCommand(), a.execCommand(),
		a.inspectCommand(), a.waitCommand(), a.portCommand(), a.cpCommand(), a.exportCommand(),
		a.statsCommand(), a.topCommand(), a.attachCommand(),
		a.unsupported("pause", "Pause all processes within one or more containers", "the container runtime cannot freeze a container's VM"),
		a.unsupported("unpause", "Unpause all processes within one or more containers", "the container runtime cannot freeze a container's VM"),
		a.unsupported("rename", "Rename a container", "the container runtime uses the name as the container's identifier"),
		a.unsupported("commit", "Create a new image from a container's changes", "the container runtime has no commit; use `build`"),
		a.unsupported("diff", "Inspect changes to files or directories on a container's filesystem", "the container runtime does not track filesystem changes"),
		a.unsupported("events", "Get real time events from the server", "the container runtime has no event stream"),
		a.unsupported("update", "Update configuration of one or more containers", "the container runtime cannot change a running container's resources"),
		a.unsupported("import", "Import the contents from a tarball to create a filesystem image", "use `image load` with an OCI archive"),
		a.imagesCommand(), a.pullCommand(), a.pushCommand(), a.tagCommand(), a.rmiCommand(),
		a.buildCommand(), a.imageCommand(), a.historyCommand(),
		a.networkCommand(), a.volumeCommand(), a.containerCommand(), a.systemCommand(),
		a.infoCommand(), a.versionCommand(), a.loginCommand(), a.logoutCommand(),
		a.composeCommand(),
	)
	return root
}

// needsRuntime reports whether a command talks to the container runtime.
func needsRuntime(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "completion", "help", "version", "compose":
			return false
		}
	}
	return true
}

func (a *App) unsupported(name, short, reason string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short + " (not supported by the container runtime)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("`%s` is not supported: %s", name, reason)
		},
	}
}

// warn prints a Docker-style warning.
func (a *App) warn(format string, args ...any) {
	a.console.Warn(format, args...)
}

// ignored warns that a flag was accepted but has no effect.
func (a *App) ignored(cmd *cobra.Command, names ...string) {
	for _, n := range names {
		if f := cmd.Flags().Lookup(n); f != nil && f.Changed {
			a.warn("--%s has no equivalent on the container runtime and is ignored", n)
		}
	}
}

// exec replaces the process with `container args...`. The caller's stdio,
// TTY and signal handling carry over unchanged, which is what attached
// commands such as run, exec and logs -f need.
func (a *App) exec(args ...string) error {
	if a.eng.DryRun || a.eng.Debug {
		fmt.Fprintf(a.console.Err, "%s container %s\n", map[bool]string{true: "[dry-run]", false: "[debug]"}[a.eng.DryRun], strings.Join(args, " "))
		if a.eng.DryRun {
			return nil
		}
	}
	argv := append([]string{a.eng.Bin}, args...)
	if a.execFn != nil {
		a.execd = argv
		return a.execFn(a.eng.Bin, argv)
	}
	progress := "none"
	if ui.IsTerminal(os.Stderr) {
		progress = "auto"
	}
	env := append(os.Environ(), "CONTAINER_PROGRESS="+progress)
	return syscall.Exec(a.eng.Bin, argv, env)
}

// runAttached runs `container args...` with the caller's stdio and returns
// its exit code as an error, for commands that need work after the runtime
// returns.
func (a *App) runAttached(ctx context.Context, args ...string) error {
	code, err := a.eng.Run(ctx, a.in, os.Stdout, os.Stderr, args...)
	if err != nil {
		return err
	}
	return exit(code)
}

func exit(code int) error {
	if code == 0 {
		return nil
	}
	return ExitCodeError{Code: code}
}

// confirm asks a yes/no question on the terminal, as Docker's prune does.
func (a *App) confirm(prompt string) bool {
	fmt.Fprintf(a.console.Out, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(a.in).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// each runs fn for every name, printing the name on success like Docker and
// collecting failures.
func (a *App) each(names []string, fn func(name string) error) error {
	var errs []error
	for _, n := range names {
		if err := fn(n); err != nil {
			fmt.Fprintf(a.console.Err, "Error response from daemon: %v\n", err)
			errs = append(errs, err)
			continue
		}
		fmt.Fprintln(a.console.Out, n)
	}
	if len(errs) > 0 {
		return ExitCodeError{Code: 1}
	}
	return nil
}
