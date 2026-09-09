package cli

import (
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) logsCommand() *cobra.Command {
	var o compose.LogsOptions
	var tail, since, until string
	var noColor bool
	cmd := &cobra.Command{
		Use:   "logs [OPTIONS] [SERVICE...]",
		Short: "View output from containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			o.Services = args
			o.Tail = -1
			if tail != "" && tail != "all" {
				n, err := strconv.Atoi(tail)
				if err != nil {
					return err
				}
				o.Tail = n
			}
			if noColor {
				a.console.Colour = false
			}
			now := time.Now()
			if o.Since, err = compose.ParseTime(since, now); err != nil {
				return err
			}
			if o.Until, err = compose.ParseTime(until, now); err != nil {
				return err
			}
			return a.runner(p).Logs(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.Follow, "follow", "f", false, "Follow log output")
	f.StringVarP(&tail, "tail", "n", "all", "Number of lines to show from the end of the logs for each container")
	f.BoolVar(&o.NoLogPrefix, "no-log-prefix", false, "Don't print prefix in logs")
	f.BoolVar(&noColor, "no-color", false, "Produce monochrome output")
	f.IntVar(&o.Index, "index", 0, "index of the container if service has multiple replicas")
	f.BoolVarP(&o.Timestamps, "timestamps", "t", false, "Show timestamps (the time each line was read; the runtime stores lines without times)")
	f.StringVar(&since, "since", "", "Show logs since timestamp (e.g. 2013-01-02T13:23:37Z) or relative (e.g. 42m for 42 minutes)")
	f.StringVar(&until, "until", "", "Show logs before a timestamp (e.g. 2013-01-02T13:23:37Z) or relative (e.g. 42m for 42 minutes)")
	return cmd
}
