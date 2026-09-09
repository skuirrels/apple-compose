package dockercli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/engine"
)

// networkRow is the shape Docker's `network ls --format json` prints.
type networkRow struct {
	ID        string `json:"ID"`
	Name      string `json:"Name"`
	Driver    string `json:"Driver"`
	Scope     string `json:"Scope"`
	IPv6      string `json:"IPv6"`
	Internal  string `json:"Internal"`
	Labels    string `json:"Labels"`
	CreatedAt string `json:"CreatedAt"`
}

func toNetworkRow(n engine.Network) networkRow {
	return networkRow{
		ID:        n.ID,
		Name:      n.Configuration.Name,
		Driver:    "bridge",
		Scope:     "local",
		IPv6:      fmt.Sprint(n.Status.IPv6Subnet != ""),
		Internal:  fmt.Sprint(n.Configuration.Mode == "internal"),
		Labels:    labelsOf(n.Configuration.Labels),
		CreatedAt: n.Configuration.CreationDate.Local().Format("2006-01-02 15:04:05 -0700 MST"),
	}
}

// networkInspectView is a Docker-shaped network record.
type networkInspectView struct {
	Name     string
	Id       string
	Created  string
	Scope    string
	Driver   string
	Internal bool
	IPAM     struct {
		Driver string
		Config []map[string]string
	}
	Options map[string]any
	Labels  map[string]any
	Runtime map[string]any `json:"Runtime"`
}

func toNetworkInspect(raw map[string]any) networkInspectView {
	cfg, _ := raw["configuration"].(map[string]any)
	status, _ := raw["status"].(map[string]any)
	v := networkInspectView{Id: fmt.Sprint(raw["id"]), Scope: "local", Driver: "bridge", Runtime: raw}
	v.Name, _ = cfg["name"].(string)
	v.Created, _ = cfg["creationDate"].(string)
	v.Internal = cfg["mode"] == "internal"
	v.Options, _ = cfg["options"].(map[string]any)
	v.Labels, _ = cfg["labels"].(map[string]any)
	v.IPAM.Driver = "default"
	entry := map[string]string{}
	if s, ok := status["ipv4Subnet"].(string); ok {
		entry["Subnet"] = s
	}
	if g, ok := status["ipv4Gateway"].(string); ok {
		entry["Gateway"] = g
	}
	if len(entry) > 0 {
		v.IPAM.Config = append(v.IPAM.Config, entry)
	}
	if s, ok := status["ipv6Subnet"].(string); ok && s != "" {
		v.IPAM.Config = append(v.IPAM.Config, map[string]string{"Subnet": s})
	}
	return v
}

func (a *App) networkCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "network", Short: "Manage networks"}

	var quiet, noTrunc bool
	var format string
	var filters []string
	ls := &cobra.Command{
		Use:     "ls [OPTIONS]",
		Aliases: []string{"list"},
		Short:   "List networks",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = noTrunc
			fl, err := parseFilters(filters)
			if err != nil {
				return err
			}
			nets, err := a.eng.ListNetworks(cmd.Context())
			if err != nil {
				return err
			}
			var items []any
			for _, n := range nets {
				if !matchNetwork(n, fl) {
					continue
				}
				if quiet {
					fmt.Fprintln(a.console.Out, n.ID)
					continue
				}
				items = append(items, toNetworkRow(n))
			}
			if quiet {
				return nil
			}
			return render(a.console.Out, format, items, []string{"NETWORK ID", "NAME", "DRIVER", "SCOPE"}, func(it any) []string {
				r := it.(networkRow)
				return []string{r.ID, r.Name, r.Driver, r.Scope}
			})
		},
	}
	ls.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only display network IDs")
	ls.Flags().BoolVar(&noTrunc, "no-trunc", false, "Do not truncate the output")
	ls.Flags().StringVar(&format, "format", "", `Format output using a custom template: 'table', 'table TEMPLATE', 'json', or a Go template`)
	ls.Flags().StringArrayVarP(&filters, "filter", "f", nil, "Provide filter values (e.g. \"name=web\")")

	var (
		driver, subnet, gateway, ipRange string
		internal, ipv6, attachable       bool
		labels, opts                     []string
	)
	create := &cobra.Command{
		Use:   "create [OPTIONS] NETWORK",
		Short: "Create a network",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if driver != "" && driver != "bridge" {
				a.warn("--driver %s is ignored: the container runtime provides one NAT network driver", driver)
			}
			if gateway != "" || ipRange != "" || ipv6 {
				a.warn("--gateway, --ip-range and --ipv6 are ignored: the container runtime assigns them")
			}
			_ = attachable
			cargs := []string{"network", "create"}
			if subnet != "" {
				cargs = append(cargs, "--subnet", subnet)
			}
			if internal {
				cargs = append(cargs, "--internal")
			}
			for _, l := range labels {
				cargs = append(cargs, "--label", l)
			}
			for _, o := range opts {
				cargs = append(cargs, "--option", o)
			}
			if _, err := a.eng.Mutate(cmd.Context(), append(cargs, args[0])...); err != nil {
				return err
			}
			fmt.Fprintln(a.console.Out, args[0])
			return nil
		},
	}
	create.Flags().StringVarP(&driver, "driver", "d", "bridge", "Driver to manage the Network")
	create.Flags().StringVar(&subnet, "subnet", "", "Subnet in CIDR format that represents a network segment")
	create.Flags().StringVar(&gateway, "gateway", "", "IPv4 or IPv6 Gateway for the master subnet")
	create.Flags().StringVar(&ipRange, "ip-range", "", "Allocate container ip from a sub-range")
	create.Flags().BoolVar(&internal, "internal", false, "Restrict external access to the network")
	create.Flags().BoolVar(&ipv6, "ipv6", false, "Enable or disable IPv6 networking")
	create.Flags().BoolVar(&attachable, "attachable", false, "Accepted for compatibility with Docker; ignored")
	_ = create.Flags().MarkHidden("attachable")
	create.Flags().StringArrayVar(&labels, "label", nil, "Set metadata on a network")
	create.Flags().StringArrayVarP(&opts, "opt", "o", nil, "Set driver specific options")

	var force bool
	rm := &cobra.Command{
		Use:     "rm NETWORK [NETWORK...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more networks",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = force
			return a.each(args, func(name string) error { return a.eng.DeleteNetwork(cmd.Context(), name) })
		},
	}
	rm.Flags().BoolVarP(&force, "force", "f", false, "Do not error if the network does not exist")

	var inspectFormat string
	inspect := &cobra.Command{
		Use:   "inspect [OPTIONS] NETWORK [NETWORK...]",
		Short: "Display detailed information on one or more networks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := a.inspectAny(cmd.Context(), args, "network")
			if err != nil {
				return err
			}
			return renderInspect(a.console.Out, inspectFormat, items)
		},
	}
	inspect.Flags().StringVarP(&inspectFormat, "format", "f", "", "Format output using a custom template")

	var pruneForce bool
	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove all unused networks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !pruneForce && !a.confirm("WARNING! This will remove all custom networks not used by at least one container.\nAre you sure you want to continue?") {
				return nil
			}
			nets, err := a.eng.ListNetworks(cmd.Context())
			if err != nil {
				return err
			}
			cs, err := a.eng.ListContainers(cmd.Context(), true)
			if err != nil {
				return err
			}
			used := map[string]bool{}
			for _, c := range cs {
				for _, n := range c.Configuration.Networks {
					used[n.Network] = true
				}
			}
			var removed []string
			for _, n := range nets {
				if n.ID == "default" || used[n.ID] || n.Label("com.apple.container.resource.role") == "builtin" {
					continue
				}
				if err := a.eng.DeleteNetwork(cmd.Context(), n.ID); err != nil {
					continue
				}
				removed = append(removed, n.ID)
			}
			if len(removed) > 0 {
				fmt.Fprintln(a.console.Out, "Deleted Networks:")
				fmt.Fprintln(a.console.Out, strings.Join(removed, "\n"))
			}
			return nil
		},
	}
	prune.Flags().BoolVarP(&pruneForce, "force", "f", false, "Do not prompt for confirmation")

	cmd.AddCommand(ls, create, rm, inspect, prune,
		a.unsupported("connect", "Connect a container to a network", "the container runtime attaches networks only at create time"),
		a.unsupported("disconnect", "Disconnect a container from a network", "the container runtime attaches networks only at create time"),
	)
	return cmd
}

func matchNetwork(n engine.Network, filters map[string][]string) bool {
	for key, values := range filters {
		ok := false
		for _, v := range values {
			switch key {
			case "name", "id":
				ok = ok || strings.Contains(n.ID, v) || strings.Contains(n.Configuration.Name, v)
			case "label":
				ok = ok || matchLabel(n.Configuration.Labels, v)
			case "driver":
				ok = ok || v == "bridge"
			case "scope":
				ok = ok || v == "local"
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
