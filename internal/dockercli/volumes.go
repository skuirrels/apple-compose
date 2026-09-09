package dockercli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/engine"
)

// volumeRow is the shape Docker's `volume ls --format json` prints.
type volumeRow struct {
	Driver     string `json:"Driver"`
	Name       string `json:"Name"`
	Mountpoint string `json:"Mountpoint"`
	Labels     string `json:"Labels"`
	Scope      string `json:"Scope"`
	Size       string `json:"Size"`
}

func toVolumeRow(v engine.Volume) volumeRow {
	return volumeRow{
		Driver:     "local",
		Name:       v.ID,
		Mountpoint: v.Configuration.Source,
		Labels:     labelsOf(v.Configuration.Labels),
		Scope:      "local",
		Size:       humanSize(v.Configuration.SizeInBytes),
	}
}

// volumeInspectView is a Docker-shaped volume record.
type volumeInspectView struct {
	Name       string
	Driver     string
	Mountpoint string
	CreatedAt  string
	Labels     map[string]any
	Options    map[string]any
	Scope      string
	Runtime    map[string]any `json:"Runtime"`
}

func toVolumeInspect(raw map[string]any) volumeInspectView {
	cfg, _ := raw["configuration"].(map[string]any)
	v := volumeInspectView{Name: fmt.Sprint(raw["id"]), Driver: "local", Scope: "local", Runtime: raw}
	v.Mountpoint, _ = cfg["source"].(string)
	v.CreatedAt, _ = cfg["creationDate"].(string)
	v.Labels, _ = cfg["labels"].(map[string]any)
	v.Options, _ = cfg["options"].(map[string]any)
	return v
}

func (a *App) volumeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "volume", Short: "Manage volumes"}

	var quiet bool
	var format string
	var filters []string
	ls := &cobra.Command{
		Use:     "ls [OPTIONS]",
		Aliases: []string{"list"},
		Short:   "List volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			fl, err := parseFilters(filters)
			if err != nil {
				return err
			}
			vols, err := a.eng.ListVolumes(cmd.Context())
			if err != nil {
				return err
			}
			var items []any
			for _, v := range vols {
				if !matchVolume(v, fl) {
					continue
				}
				if quiet {
					fmt.Fprintln(a.console.Out, v.ID)
					continue
				}
				items = append(items, toVolumeRow(v))
			}
			if quiet {
				return nil
			}
			return render(a.console.Out, format, items, []string{"DRIVER", "VOLUME NAME"}, func(it any) []string {
				r := it.(volumeRow)
				return []string{r.Driver, r.Name}
			})
		},
	}
	ls.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only display volume names")
	ls.Flags().StringVar(&format, "format", "", `Format output using a custom template: 'table', 'table TEMPLATE', 'json', or a Go template`)
	ls.Flags().StringArrayVarP(&filters, "filter", "f", nil, "Provide filter values (e.g. \"dangling=true\")")

	var driver string
	var labels, opts []string
	create := &cobra.Command{
		Use:   "create [OPTIONS] [VOLUME]",
		Short: "Create a volume",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if driver != "" && driver != "local" {
				a.warn("--driver %s is ignored: the container runtime has one volume driver", driver)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				b := make([]byte, 32)
				_, _ = rand.Read(b)
				name = hex.EncodeToString(b)
			}
			cargs := []string{"volume", "create"}
			for _, l := range labels {
				cargs = append(cargs, "--label", l)
			}
			for _, o := range opts {
				if k, v, ok := strings.Cut(o, "="); ok && k == "size" {
					cargs = append(cargs, "-s", v)
					continue
				}
				cargs = append(cargs, "--opt", o)
			}
			if _, err := a.eng.Mutate(cmd.Context(), append(cargs, name)...); err != nil {
				return err
			}
			fmt.Fprintln(a.console.Out, name)
			return nil
		},
	}
	create.Flags().StringVarP(&driver, "driver", "d", "local", "Specify volume driver name")
	create.Flags().StringArrayVar(&labels, "label", nil, "Set metadata for a volume")
	create.Flags().StringArrayVarP(&opts, "opt", "o", nil, "Set driver specific options")

	var force bool
	rm := &cobra.Command{
		Use:     "rm [OPTIONS] VOLUME [VOLUME...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more volumes",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = force
			return a.each(args, func(name string) error { return a.eng.DeleteVolume(cmd.Context(), name) })
		},
	}
	rm.Flags().BoolVarP(&force, "force", "f", false, "Force the removal of one or more volumes")

	var inspectFormat string
	inspect := &cobra.Command{
		Use:   "inspect [OPTIONS] VOLUME [VOLUME...]",
		Short: "Display detailed information on one or more volumes",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := a.inspectAny(cmd.Context(), args, "volume")
			if err != nil {
				return err
			}
			return renderInspect(a.console.Out, inspectFormat, items)
		},
	}
	inspect.Flags().StringVarP(&inspectFormat, "format", "f", "", "Format output using a custom template")

	var pruneForce, pruneAll bool
	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused local volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = pruneAll
			if !pruneForce && !a.confirm("WARNING! This will remove all local volumes not used by at least one container.\nAre you sure you want to continue?") {
				return nil
			}
			return a.runAttached(cmd.Context(), "volume", "prune")
		},
	}
	prune.Flags().BoolVarP(&pruneForce, "force", "f", false, "Do not prompt for confirmation")
	prune.Flags().BoolVarP(&pruneAll, "all", "a", false, "Accepted for compatibility with Docker; ignored")
	_ = prune.Flags().MarkHidden("all")

	cmd.AddCommand(ls, create, rm, inspect, prune)
	return cmd
}

func matchVolume(v engine.Volume, filters map[string][]string) bool {
	for key, values := range filters {
		ok := false
		for _, want := range values {
			switch key {
			case "name":
				ok = ok || strings.Contains(v.ID, want)
			case "label":
				ok = ok || matchLabel(v.Configuration.Labels, want)
			case "driver":
				ok = ok || want == "local"
			case "dangling":
				ok = true
			default:
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
