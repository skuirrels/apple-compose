package cli

import (
	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) psCommand() *cobra.Command {
	var o compose.PsOptions
	var filter string
	cmd := &cobra.Command{
		Use:   "ps [OPTIONS] [SERVICE...]",
		Short: "List containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			o.Services = args
			if filter != "" {
				if k, v, ok := cutKV(filter); ok && k == "status" {
					o.Status = append(o.Status, v)
				}
			}
			return a.runner(p).Ps(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.All, "all", "a", false, "Show all stopped containers (including those created by the run command)")
	f.StringArrayVar(&o.Status, "status", nil, "Filter services by status. Values: [paused | restarting | removing | running | dead | created | exited]")
	f.StringVar(&filter, "filter", "", "Filter services by a property (supported filters: status)")
	f.BoolVarP(&o.Quiet, "quiet", "q", false, "Only display IDs")
	f.BoolVar(&o.ServicesOnly, "services", false, "Display services")
	f.StringVar(&o.Format, "format", "table", `Format output using a custom template: 'table' or 'json'`)
	f.BoolVar(&o.Orphans, "orphans", true, "Include orphaned services (not declared by project)")
	f.BoolVar(&o.Health, "health", false, "Probe healthchecks and show health in the status column")
	f.Bool("no-trunc", false, "Accepted for compatibility")
	_ = f.MarkHidden("no-trunc")
	return cmd
}

func cutKV(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
