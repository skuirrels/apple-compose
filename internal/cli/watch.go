package cli

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) watchCommand() *cobra.Command {
	var o compose.WatchOptions
	cmd := &cobra.Command{
		Use:   "watch [OPTIONS] [SERVICE...]",
		Short: "Watch build context for service and rebuild/refresh containers when files are updated",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), args, true)
			if err != nil {
				return err
			}
			o.Services = args
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.runner(p).Watch(ctx, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.NoUp, "no-up", false, "Do not build and start services before watching")
	f.BoolVar(&o.Prune, "prune", false, "Delete synchronised files inside the container when they are deleted on the host")
	f.DurationVar(&o.Interval, "interval", compose.DefaultWatchInterval, "How often the watched paths are rescanned")
	f.Bool("quiet", false, "Accepted for compatibility with Docker Compose")
	_ = f.MarkHidden("quiet")
	return cmd
}

// watchAfterUp keeps watching once `up --watch` has started the services.
func (a *App) watchAfterUp(cmd *cobra.Command, r *compose.Runner, services []string, interval time.Duration) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return r.Watch(ctx, compose.WatchOptions{Services: services, NoUp: true, Interval: interval})
}
