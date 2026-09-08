package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) downCommand() *cobra.Command {
	var o compose.DownOptions
	var timeout int
	cmd := &cobra.Command{
		Use:   "down [OPTIONS] [SERVICES]",
		Short: "Stop and remove containers, networks",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			o.Services = args
			o.Timeout = time.Duration(timeout) * time.Second
			return a.runner(p).Down(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.RemoveOrphans, "remove-orphans", false, "Remove containers for services not defined in the Compose file")
	f.StringVar(&o.RemoveImages, "rmi", "", `Remove images used by services. "local" remove only images that don't have a custom tag ("local"|"all")`)
	f.BoolVarP(&o.Volumes, "volumes", "v", false, "Remove named volumes declared in the volumes section of the Compose file")
	f.IntVarP(&timeout, "timeout", "t", 0, "Specify a shutdown timeout in seconds")
	return cmd
}
