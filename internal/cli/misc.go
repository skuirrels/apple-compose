package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
	"github.com/skuirrels/apple-compose/internal/ui"
)

func (a *App) lsCommand() *cobra.Command {
	var all, quiet bool
	var format string
	cmd := &cobra.Command{
		Use:   "ls [OPTIONS]",
		Short: "List running compose projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := compose.ListProjects(cmd.Context(), a.eng, all)
			if err != nil {
				return err
			}
			switch {
			case quiet:
				for _, p := range projects {
					fmt.Fprintln(os.Stdout, p.Name)
				}
			case format == "json":
				if projects == nil {
					projects = []compose.ProjectSummary{}
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(projects)
			default:
				var rows [][]string
				for _, p := range projects {
					rows = append(rows, []string{p.Name, p.Status, p.ConfigFiles})
				}
				ui.Table(os.Stdout, []string{"NAME", "STATUS", "CONFIG FILES"}, rows)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show all stopped Compose projects")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only display project names")
	cmd.Flags().StringVar(&format, "format", "table", `Format the output ("table"|"json")`)
	return cmd
}

func (a *App) portCommand() *cobra.Command {
	var index int
	var protocol string
	cmd := &cobra.Command{
		Use:   "port [OPTIONS] SERVICE PRIVATE_PORT",
		Short: "Print the public port for a port binding",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			port, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid port %q", args[1])
			}
			out, err := a.runner(p).Port(cmd.Context(), args[0], index, port, protocol)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, out)
			return nil
		},
	}
	cmd.Flags().IntVar(&index, "index", 0, "Index of the container if service has multiple replicas")
	cmd.Flags().StringVar(&protocol, "protocol", "tcp", "tcp or udp")
	return cmd
}

func (a *App) cpCommand() *cobra.Command {
	var index int
	cmd := &cobra.Command{
		Use:   "cp [OPTIONS] SERVICE:SRC_PATH DEST_PATH|-\n  apple-compose cp [OPTIONS] SRC_PATH|- SERVICE:DEST_PATH",
		Short: "Copy files/folders between a service container and the local filesystem",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			return a.runner(p).Copy(cmd.Context(), args[0], args[1], index)
		},
	}
	cmd.Flags().IntVar(&index, "index", 0, "Index of the container if service has multiple replicas")
	cmd.Flags().Bool("all", false, "Accepted for compatibility")
	_ = cmd.Flags().MarkHidden("all")
	return cmd
}

func (a *App) waitCommand() *cobra.Command {
	var downProject bool
	cmd := &cobra.Command{
		Use:   "wait SERVICE [SERVICE...] [OPTIONS]",
		Short: "Block until containers of all (or specified) services stop",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			code, err := a.runner(p).Wait(cmd.Context(), args, downProject)
			if err != nil {
				return err
			}
			return exit(code)
		},
	}
	cmd.Flags().BoolVar(&downProject, "down-project", false, "Drops project when the first container stops")
	return cmd
}

func (a *App) topCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "top [SERVICES...]",
		Short: "Display the running processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			r := a.runner(p)
			rows, err := r.Rows(cmd.Context(), compose.PsOptions{Services: args})
			if err != nil {
				return err
			}
			for _, row := range rows {
				fmt.Fprintf(os.Stdout, "%s\n", row.Name)
				code, err := a.eng.Exec(cmd.Context(), row.Name, compose.PlainExec(), []string{"ps", "-eo", "pid,user,etime,args"}, nil, os.Stdout, os.Stderr)
				if err != nil || code != 0 {
					fmt.Fprintf(os.Stdout, "  (ps is not available in this image)\n")
				}
				fmt.Fprintln(os.Stdout)
			}
			return nil
		},
	}
}

func (a *App) eventsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "events [SERVICE...]",
		Short: "Receive real time events from containers (not supported by the container runtime)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("`events` is not supported: the container runtime has no event stream")
		},
	}
}

func (a *App) versionCommand() *cobra.Command {
	var short bool
	var format string
	cmd := &cobra.Command{
		Use:   "version [OPTIONS]",
		Short: "Show the apple-compose and container runtime versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := "not found"
			if a.eng != nil {
				if v, err := a.eng.Version(cmd.Context()); err == nil {
					rt = v
				}
			}
			switch {
			case short:
				fmt.Fprintln(os.Stdout, a.version)
			case format == "json":
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"version": a.version, "container": rt, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH})
			default:
				fmt.Fprintf(os.Stdout, "apple-compose version %s\ncontainer runtime %s\n", a.version, rt)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "Shows only apple-compose's version number")
	cmd.Flags().StringVarP(&format, "format", "f", "pretty", `Format the output ("pretty"|"json")`)
	return cmd
}
