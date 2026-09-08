package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func (a *App) startCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start [SERVICE...]",
		Short: "Start services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			return a.runner(p).Start(cmd.Context(), args)
		},
	}
}

func (a *App) stopCommand() *cobra.Command {
	var timeout int
	cmd := &cobra.Command{
		Use:   "stop [OPTIONS] [SERVICE...]",
		Short: "Stop services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			return a.runner(p).Stop(cmd.Context(), args, time.Duration(timeout)*time.Second)
		},
	}
	cmd.Flags().IntVarP(&timeout, "timeout", "t", 0, "Specify a shutdown timeout in seconds")
	return cmd
}

func (a *App) restartCommand() *cobra.Command {
	var timeout int
	var noDeps bool
	cmd := &cobra.Command{
		Use:   "restart [OPTIONS] [SERVICE...]",
		Short: "Restart service containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			return a.runner(p).Restart(cmd.Context(), args, time.Duration(timeout)*time.Second, noDeps)
		},
	}
	cmd.Flags().IntVarP(&timeout, "timeout", "t", 0, "Specify a shutdown timeout in seconds")
	cmd.Flags().BoolVar(&noDeps, "no-deps", false, "Don't restart dependent services")
	return cmd
}

func (a *App) rmCommand() *cobra.Command {
	var force, stop, volumes bool
	cmd := &cobra.Command{
		Use:   "rm [OPTIONS] [SERVICE...]",
		Short: "Removes stopped service containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			return a.runner(p).Rm(cmd.Context(), args, force, stop, volumes)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Don't ask to confirm removal")
	cmd.Flags().BoolVarP(&stop, "stop", "s", false, "Stop the containers, if required, before removing")
	cmd.Flags().BoolVarP(&volumes, "volumes", "v", false, "Remove any anonymous volumes attached to containers")
	return cmd
}

func (a *App) killCommand() *cobra.Command {
	var signal string
	var removeOrphans bool
	cmd := &cobra.Command{
		Use:   "kill [OPTIONS] [SERVICE...]",
		Short: "Force stop service containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			return a.runner(p).Kill(cmd.Context(), args, signal)
		},
	}
	cmd.Flags().StringVarP(&signal, "signal", "s", "SIGKILL", "SIGNAL to send to the container")
	cmd.Flags().BoolVar(&removeOrphans, "remove-orphans", false, "Accepted for compatibility")
	_ = cmd.Flags().MarkHidden("remove-orphans")
	return cmd
}

func (a *App) pauseCommand(name string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " [SERVICE...]",
		Short: fmt.Sprintf("%s services (not supported by the container runtime)", capitalise(name)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("`%s` is not supported: the container runtime cannot freeze a container's VM", name)
		},
	}
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}
