package cli

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

// superviseCommand is the background process `up -d` launches to honour
// restart policies; it is hidden because users do not run it by hand.
func (a *App) superviseCommand() *cobra.Command {
	var once bool
	var interval time.Duration
	cmd := &cobra.Command{
		Use:    "supervise",
		Short:  "Restart exited containers according to their restart policies (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				if a.g.projectName == "" {
					return err
				}
				// Supervision must not depend on the compose file staying
				// loadable; hosts aliases degrade but restarts still work.
				a.console.Warn("cannot load the compose project (%v); supervising by labels only", err)
				p = &types.Project{Name: a.g.projectName, Services: types.Services{}}
			}
			lock, err := compose.HoldSupervisorLock(p.Name)
			if err != nil {
				return err
			}
			defer lock.Close()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			a.console.Colour = false
			a.console.Err = compose.Timestamped(a.console.Err)
			a.console.Info("supervising project %s", p.Name)
			err = a.runner(p).Supervise(ctx, compose.SuperviseOptions{Interval: interval, Once: once})
			if ctx.Err() != nil {
				a.console.Info("supervisor stopped")
				return nil
			}
			a.console.Info("nothing left to supervise")
			return err
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "Make a single pass and exit")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Polling interval")
	return cmd
}
