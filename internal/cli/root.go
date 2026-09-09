// Package cli defines the apple-compose command tree with Docker Compose's
// command names and flags wherever the runtime can honour them.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// globals are the flags shared by every command.
type globals struct {
	files       []string
	projectName string
	projectDir  string
	envFiles    []string
	profiles    []string
	ansi        string
	progress    string
	dryRun      bool
	debug       bool
	parallel    int
	allServices bool
}

// App wires the command tree together.
type App struct {
	version string
	g       globals
	console *ui.Console
	eng     *engine.Engine
	root    *cobra.Command
}

// ExitCodeError carries a process exit code out of a command.
type ExitCodeError struct{ Code int }

func (e ExitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Execute runs the CLI and returns the process exit code.
func Execute(version string, args []string) int {
	app := &App{version: version}
	app.root = app.rootCommand()
	app.root.SetArgs(args)
	if err := app.root.Execute(); err != nil {
		var ec ExitCodeError
		if errors.As(err, &ec) {
			return ec.Code
		}
		if app.console != nil {
			fmt.Fprintln(app.console.Err, app.console.Paint("31", "error:"), err)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		return 1
	}
	return 0
}

// invokedAsPlugin reports whether we were launched by `container compose`.
func invokedAsPlugin() bool {
	base := filepath.Base(os.Args[0])
	return base == "compose"
}

func (a *App) rootCommand() *cobra.Command {
	use := "apple-compose"
	if invokedAsPlugin() {
		use = "container compose"
	}
	root := &cobra.Command{
		Use:           use,
		Short:         "Define and run multi-container applications on Apple's container runtime",
		Long:          "apple-compose reads standard Compose files and runs them on Apple's `container` runtime for macOS, with Docker Compose's commands and flags.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Global flags live on the root command and are parsed before the
		// subcommand, as Docker Compose does, so `logs -f` and `run -p` can
		// reuse the shorthands that `--file` and `--project-name` take here.
		TraverseChildren: true,
		Version:          a.version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			a.console = ui.NewConsole(a.g.ansi)
			eng, err := engine.New()
			if err != nil {
				if cmd.Name() == "version" || cmd.Name() == "config" || cmd.Name() == "help" {
					return nil
				}
				return err
			}
			eng.DryRun = a.g.dryRun
			eng.Debug = a.g.debug
			eng.Log = a.console.Err
			a.eng = eng
			return nil
		},
	}
	root.SetVersionTemplate("apple-compose version {{.Version}}\n")
	root.Long += "\n\nGlobal options (--file, --project-name, --profile, ...) go before the subcommand:\n  apple-compose -f compose.yaml -p myapp up -d\n"
	pf := root.Flags()
	pf.StringArrayVarP(&a.g.files, "file", "f", nil, "Compose configuration files")
	pf.StringVarP(&a.g.projectName, "project-name", "p", "", "Project name")
	pf.StringVar(&a.g.projectDir, "project-directory", "", "Specify an alternate working directory (default: the path of the first Compose file)")
	pf.StringArrayVar(&a.g.envFiles, "env-file", nil, "Specify an alternate environment file")
	pf.StringArrayVar(&a.g.profiles, "profile", nil, "Specify a profile to enable")
	pf.StringVar(&a.g.ansi, "ansi", "auto", `Control when to print ANSI control characters ("never"|"always"|"auto")`)
	pf.StringVar(&a.g.progress, "progress", "auto", "Set type of progress output (accepted for compatibility)")
	pf.BoolVar(&a.g.dryRun, "dry-run", false, "Print the container commands that would run without executing them")
	pf.BoolVar(&a.g.debug, "debug", false, "Print every container command as it runs")
	pf.IntVar(&a.g.parallel, "parallel", 4, "Maximum number of services started concurrently")
	pf.BoolVar(&a.g.allServices, "all-resources", false, "Include services disabled by profiles")
	pf.Bool("compatibility", false, "Accepted for compatibility with Docker Compose")
	_ = pf.MarkHidden("compatibility")

	root.AddCommand(
		a.upCommand(), a.downCommand(), a.psCommand(), a.logsCommand(),
		a.startCommand(), a.stopCommand(), a.restartCommand(), a.rmCommand(), a.killCommand(),
		a.pauseCommand("pause"), a.pauseCommand("unpause"),
		a.execCommand(), a.runCommand(), a.createCommand(),
		a.pullCommand(), a.pushCommand(), a.buildCommand(), a.imagesCommand(),
		a.configCommand(), a.lsCommand(), a.portCommand(), a.cpCommand(), a.waitCommand(),
		a.topCommand(), a.eventsCommand(), a.versionCommand(), a.pluginCommand(),
	)
	return root
}

// loadProject reads the compose project for a command, selecting services
// when the command names some.
func (a *App) loadProject(ctx context.Context, services []string, withDeps bool) (*types.Project, error) {
	profiles := a.g.profiles
	if len(profiles) == 0 {
		if env := os.Getenv("COMPOSE_PROFILES"); env != "" {
			profiles = strings.Split(env, ",")
		}
	}
	p, err := project.Load(ctx, project.Options{
		ConfigPaths: a.g.files,
		ProjectName: a.g.projectName,
		WorkingDir:  a.g.projectDir,
		EnvFiles:    a.g.envFiles,
		Profiles:    profiles,
		AllServices: a.g.allServices,
	}, a.version)
	if err != nil {
		return nil, err
	}
	return project.Select(p, services, withDeps)
}

// loadProjectOrName loads the project, but when no compose file is found and
// a project name was given, returns a bare project so file-less commands such
// as `down -p name` still work.
func (a *App) loadProjectOrName(ctx context.Context, services []string, withDeps bool) (*types.Project, error) {
	p, err := a.loadProject(ctx, services, withDeps)
	if err == nil {
		return p, nil
	}
	if a.g.projectName != "" && len(a.g.files) == 0 && isNoComposeFile(err) {
		return &types.Project{Name: a.g.projectName, Services: types.Services{}}, nil
	}
	return nil, err
}

func isNoComposeFile(err error) bool {
	s := err.Error()
	return strings.Contains(s, "no configuration file provided") || strings.Contains(s, "not found")
}

// runner builds a Runner for the project.
func (a *App) runner(p *types.Project) *compose.Runner {
	return compose.New(a.eng, p, a.console, a.version)
}

func exit(code int) error {
	if code == 0 {
		return nil
	}
	return ExitCodeError{Code: code}
}
