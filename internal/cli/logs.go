package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) logsCommand() *cobra.Command {
	var o compose.LogsOptions
	var tail string
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
			for _, name := range []string{"timestamps", "since", "until"} {
				if fl := cmd.Flags().Lookup(name); fl != nil && fl.Changed {
					a.console.Warn("--%s is not supported by the container runtime's log store and is ignored", name)
				}
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
	f.BoolP("timestamps", "t", false, "Show timestamps (not supported by the runtime)")
	f.String("since", "", "Show logs since timestamp (not supported by the runtime)")
	f.String("until", "", "Show logs before a timestamp (not supported by the runtime)")
	return cmd
}
