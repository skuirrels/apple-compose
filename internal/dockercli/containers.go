package dockercli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// psRow is the shape Docker's `ps --format json` prints.
type psRow struct {
	ID         string `json:"ID"`
	Image      string `json:"Image"`
	Command    string `json:"Command"`
	CreatedAt  string `json:"CreatedAt"`
	RunningFor string `json:"RunningFor"`
	Ports      string `json:"Ports"`
	State      string `json:"State"`
	Status     string `json:"Status"`
	Names      string `json:"Names"`
	Labels     string `json:"Labels"`
	Networks   string `json:"Networks"`
	Mounts     string `json:"Mounts"`
	Size       string `json:"Size"`
	labels     map[string]string
	created    time.Time
}

func containerState(c engine.Container) string {
	if c.Status.State == "stopped" {
		return "exited"
	}
	return c.Status.State
}

func containerStatus(c engine.Container) string {
	switch containerState(c) {
	case "running":
		return "Up " + ui.HumanDuration(time.Since(c.Status.StartedDate))
	case "exited":
		if c.Status.StartedDate.IsZero() {
			return "Created"
		}
		return "Exited"
	default:
		return ui.Capitalise(c.Status.State)
	}
}

func portsOf(c engine.Container) string {
	var out []string
	for _, p := range c.Configuration.PublishedPorts {
		addr := p.HostAddress
		if addr == "" {
			addr = "0.0.0.0"
		}
		out = append(out, fmt.Sprintf("%s:%d->%d/%s", addr, p.HostPort, p.ContainerPort, p.Proto))
	}
	return strings.Join(out, ", ")
}

func labelsOf(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, k+"="+labels[k])
	}
	return strings.Join(out, ",")
}

func mountsOf(c engine.Container) string {
	var out []string
	for _, m := range c.Configuration.Mounts {
		if d, ok := m["destination"].(string); ok {
			out = append(out, d)
		}
	}
	return strings.Join(out, ",")
}

func toPsRow(c engine.Container, noTrunc bool) psRow {
	var nets []string
	for _, n := range c.Configuration.Networks {
		nets = append(nets, n.Network)
	}
	cmd := c.Command()
	if !noTrunc && len(cmd) > 20 {
		cmd = cmd[:19] + "…"
	}
	return psRow{
		ID:         c.ID,
		Image:      ui.DisplayImage(c.Configuration.Image.Reference),
		Command:    `"` + cmd + `"`,
		CreatedAt:  c.Configuration.CreationDate.Local().Format("2006-01-02 15:04:05 -0700 MST"),
		RunningFor: ui.HumanDuration(time.Since(c.Configuration.CreationDate)) + " ago",
		Ports:      portsOf(c),
		State:      containerState(c),
		Status:     containerStatus(c),
		Names:      c.ID,
		Labels:     labelsOf(c.Configuration.Labels),
		Networks:   strings.Join(nets, ","),
		Mounts:     mountsOf(c),
		labels:     c.Configuration.Labels,
		created:    c.Configuration.CreationDate,
	}
}

// listContainers applies Docker's ps filters.
func (a *App) listContainers(ctx context.Context, all bool, filters map[string][]string) ([]engine.Container, error) {
	cs, err := a.eng.ListContainers(ctx, all || len(filters["status"]) > 0)
	if err != nil {
		return nil, err
	}
	var out []engine.Container
	for _, c := range cs {
		if !matchContainer(c, filters) {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Configuration.CreationDate.After(out[j].Configuration.CreationDate)
	})
	return out, nil
}

func matchContainer(c engine.Container, filters map[string][]string) bool {
	for key, values := range filters {
		ok := false
		for _, v := range values {
			switch key {
			case "name", "id":
				ok = ok || strings.Contains(c.ID, v)
			case "status":
				ok = ok || containerState(c) == v || (v == "created" && containerStatus(c) == "Created")
			case "label":
				ok = ok || matchLabel(c.Configuration.Labels, v)
			case "ancestor":
				ok = ok || ui.DisplayImage(c.Configuration.Image.Reference) == ui.DisplayImage(v) || strings.HasPrefix(ui.DisplayImage(c.Configuration.Image.Reference), ui.DisplayImage(v)+":")
			case "network":
				for _, n := range c.Configuration.Networks {
					ok = ok || n.Network == v
				}
			case "volume":
				for _, m := range c.Configuration.Mounts {
					if s, _ := m["source"].(string); strings.Contains(s, v) {
						ok = true
					}
					if d, _ := m["destination"].(string); d == v {
						ok = true
					}
				}
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

func (a *App) psCommand() *cobra.Command {
	var (
		all, quiet, noTrunc, latest, size bool
		last                              int
		format                            string
		filters                           []string
	)
	cmd := &cobra.Command{
		Use:   "ps [OPTIONS]",
		Short: "List containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			fl, err := parseFilters(filters)
			if err != nil {
				return err
			}
			if latest {
				last = 1
			}
			cs, err := a.listContainers(cmd.Context(), all || last > 0, fl)
			if err != nil {
				return err
			}
			if last > 0 && len(cs) > last {
				cs = cs[:last]
			}
			if size {
				a.warn("--size is not available: the container runtime does not report writable layer sizes")
			}
			if quiet {
				for _, c := range cs {
					fmt.Fprintln(a.console.Out, c.ID)
				}
				return nil
			}
			items := make([]any, 0, len(cs))
			for _, c := range cs {
				items = append(items, toPsRow(c, noTrunc))
			}
			return render(a.console.Out, format, items,
				[]string{"CONTAINER ID", "IMAGE", "COMMAND", "CREATED", "STATUS", "PORTS", "NAMES"},
				func(it any) []string {
					r := it.(psRow)
					return []string{r.ID, r.Image, r.Command, r.RunningFor, r.Status, r.Ports, r.Names}
				})
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&all, "all", "a", false, "Show all containers (default shows just running)")
	f.BoolVarP(&quiet, "quiet", "q", false, "Only display container IDs")
	f.BoolVar(&noTrunc, "no-trunc", false, "Don't truncate output")
	f.BoolVarP(&latest, "latest", "l", false, "Show the latest created container (includes all states)")
	f.IntVarP(&last, "last", "n", 0, "Show n last created containers (includes all states)")
	f.BoolVarP(&size, "size", "s", false, "Display total file sizes")
	f.StringVar(&format, "format", "", `Format output using a custom template: 'table', 'table TEMPLATE', 'json', or a Go template`)
	f.StringArrayVarP(&filters, "filter", "f", nil, "Filter output based on conditions provided")
	return cmd
}

func (a *App) startCommand() *cobra.Command {
	var attach, interactive bool
	cmd := &cobra.Command{
		Use:   "start [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Start one or more stopped containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if attach || interactive {
				if len(args) > 1 {
					return errors.New("you cannot start and attach multiple containers at once")
				}
				sargs := []string{"start", "--attach"}
				if interactive {
					sargs = append(sargs, "--interactive")
				}
				return a.exec(append(sargs, args[0])...)
			}
			clearStopped(args...)
			err := a.each(args, func(name string) error { return a.eng.Start(cmd.Context(), name) })
			a.ensureSupervisor(cmd.Context())
			return err
		},
	}
	cmd.Flags().BoolVarP(&attach, "attach", "a", false, "Attach STDOUT/STDERR and forward signals")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Attach container's STDIN")
	cmd.Flags().String("detach-keys", "", "Accepted for compatibility with Docker; ignored")
	_ = cmd.Flags().MarkHidden("detach-keys")
	return cmd
}

func (a *App) stopCommand() *cobra.Command {
	var timeout int
	var signal string
	cmd := &cobra.Command{
		Use:   "stop [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Stop one or more running containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.each(args, func(name string) error {
				if err := a.eng.Stop(cmd.Context(), []string{name}, signal, time.Duration(timeout)*time.Second); err != nil {
					return err
				}
				markStopped(name)
				return nil
			})
		},
	}
	cmd.Flags().IntVarP(&timeout, "time", "t", 10, "Seconds to wait before killing the container")
	cmd.Flags().StringVarP(&signal, "signal", "s", "", "Signal to send to the container")
	return cmd
}

func (a *App) restartCommand() *cobra.Command {
	var timeout int
	var signal string
	cmd := &cobra.Command{
		Use:   "restart [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Restart one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := a.each(args, func(name string) error {
				c, err := a.eng.InspectContainer(cmd.Context(), name)
				if err != nil {
					return err
				}
				if c.Running() {
					if err := a.eng.Stop(cmd.Context(), []string{name}, signal, time.Duration(timeout)*time.Second); err != nil {
						return err
					}
				}
				clearStopped(name)
				return a.eng.Start(cmd.Context(), name)
			})
			a.ensureSupervisor(cmd.Context())
			return err
		},
	}
	cmd.Flags().IntVarP(&timeout, "time", "t", 10, "Seconds to wait before killing the container")
	cmd.Flags().StringVarP(&signal, "signal", "s", "", "Signal to send to the container")
	return cmd
}

func (a *App) killCommand() *cobra.Command {
	var signal string
	cmd := &cobra.Command{
		Use:   "kill [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Kill one or more running containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.each(args, func(name string) error {
				if err := a.eng.Kill(cmd.Context(), []string{name}, signal); err != nil {
					return err
				}
				markStopped(name)
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&signal, "signal", "s", "KILL", "Signal to send to the container")
	return cmd
}

func (a *App) rmCommand() *cobra.Command {
	var force, volumes, link bool
	cmd := &cobra.Command{
		Use:     "rm [OPTIONS] CONTAINER [CONTAINER...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more containers",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if link {
				return errors.New("--link is not supported: the container runtime has no links")
			}
			if volumes {
				a.warn("--volumes is ignored: the container runtime does not track anonymous volumes per container")
			}
			return a.each(args, func(name string) error { return a.eng.Delete(cmd.Context(), []string{name}, force) })
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force the removal of a running container (uses SIGKILL)")
	cmd.Flags().BoolVarP(&volumes, "volumes", "v", false, "Remove anonymous volumes associated with the container")
	cmd.Flags().BoolVarP(&link, "link", "l", false, "Remove the specified link")
	return cmd
}

func (a *App) logsCommand() *cobra.Command {
	var follow, timestamps, details bool
	var tail, since, until string
	cmd := &cobra.Command{
		Use:   "logs [OPTIONS] CONTAINER",
		Short: "Fetch the logs of a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if since != "" || until != "" {
				a.warn("--since/--until are ignored: the container runtime stores log lines without timestamps")
			}
			_ = details
			largs := []string{"logs"}
			if follow {
				largs = append(largs, "--follow")
			}
			if tail != "" && tail != "all" {
				n, err := strconv.Atoi(tail)
				if err != nil {
					return fmt.Errorf("invalid --tail %q", tail)
				}
				largs = append(largs, "-n", strconv.Itoa(n))
			}
			largs = append(largs, args[0])
			if !timestamps {
				return a.exec(largs...)
			}
			// Stamps are the time each line was read; the runtime keeps none.
			c := a.eng.Command(cmd.Context(), largs...)
			pr, pw := io.Pipe()
			c.Stdout = pw
			c.Stderr = pw
			engine.Detach(c)
			if err := c.Start(); err != nil {
				return err
			}
			go func() {
				_ = c.Wait()
				pw.Close()
			}()
			rd := bufio.NewReader(pr)
			for {
				line, err := rd.ReadString('\n')
				if line != "" {
					fmt.Fprintf(a.console.Out, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSuffix(line, "\n"))
				}
				if err != nil {
					return nil
				}
			}
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&follow, "follow", "f", false, "Follow log output")
	f.BoolVarP(&timestamps, "timestamps", "t", false, "Show timestamps")
	f.BoolVar(&details, "details", false, "Accepted for compatibility with Docker; ignored")
	_ = f.MarkHidden("details")
	f.StringVarP(&tail, "tail", "n", "all", "Number of lines to show from the end of the logs")
	f.StringVar(&since, "since", "", "Show logs since timestamp (not supported by the runtime)")
	f.StringVar(&until, "until", "", "Show logs before a timestamp (not supported by the runtime)")
	return cmd
}

func (a *App) execCommand() *cobra.Command {
	var (
		detach, interactive, tty, privileged bool
		user, workdir                        string
		env, envFiles                        []string
	)
	cmd := &cobra.Command{
		Use:   "exec [OPTIONS] CONTAINER COMMAND [ARG...]",
		Short: "Execute a command in a running container",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if privileged {
				a.warn("--privileged is ignored for exec")
			}
			eargs := []string{"exec"}
			if detach {
				eargs = append(eargs, "--detach")
			}
			if interactive && !detach {
				eargs = append(eargs, "--interactive")
			}
			if tty && !detach {
				eargs = append(eargs, "--tty")
			}
			if user != "" {
				eargs = append(eargs, "--user", user)
			}
			if workdir != "" {
				eargs = append(eargs, "--workdir", workdir)
			}
			for _, e := range env {
				eargs = append(eargs, "--env", e)
			}
			for _, f := range envFiles {
				eargs = append(eargs, "--env-file", f)
			}
			a.ignored(cmd, "detach-keys")
			return a.exec(append(eargs, args...)...)
		},
	}
	cmd.Flags().SetInterspersed(false)
	f := cmd.Flags()
	f.BoolVarP(&detach, "detach", "d", false, "Detached mode: run command in the background")
	f.BoolVarP(&interactive, "interactive", "i", false, "Keep STDIN open even if not attached")
	f.BoolVarP(&tty, "tty", "t", false, "Allocate a pseudo-TTY")
	f.BoolVar(&privileged, "privileged", false, "Give extended privileges to the command")
	f.StringVarP(&user, "user", "u", "", "Username or UID (format: <name|uid>[:<group|gid>])")
	f.StringVarP(&workdir, "workdir", "w", "", "Working directory inside the container")
	f.StringArrayVarP(&env, "env", "e", nil, "Set environment variables")
	f.StringArrayVar(&envFiles, "env-file", nil, "Read in a file of environment variables")
	f.String("detach-keys", "", "Accepted for compatibility with Docker; ignored")
	_ = f.MarkHidden("detach-keys")
	return cmd
}

// inspectView is the Docker-shaped inspect record; the runtime's own
// record rides along under Runtime for anything not mapped.
type inspectView struct {
	Id      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Path    string `json:"Path"`
	Args    []string
	Image   string `json:"Image"`
	State   struct {
		Status     string
		Running    bool
		Paused     bool
		Restarting bool
		ExitCode   int
		StartedAt  string
	}
	Config struct {
		Hostname   string
		User       string
		Env        []string
		Cmd        []string
		Image      string
		WorkingDir string
		Labels     map[string]string
		Tty        bool
	}
	HostConfig struct {
		NetworkMode    string
		ReadonlyRootfs bool
		Init           bool
		Memory         int64
		NanoCpus       int64
		CpuCount       int
		PortBindings   map[string][]portBinding
	}
	Mounts          []mountView
	NetworkSettings struct {
		IPAddress  string
		Gateway    string
		MacAddress string
		Ports      map[string][]portBinding
		Networks   map[string]networkView
	}
	Runtime json.RawMessage `json:"Runtime"`
}

type portBinding struct {
	HostIp   string
	HostPort string
}

type mountView struct {
	Type        string
	Source      string
	Destination string
	RW          bool
}

type networkView struct {
	IPAddress            string
	Gateway              string
	MacAddress           string
	GlobalIPv6Address    string
	NetworkID            string
	Aliases              []string
	IPPrefixLen          int
	EndpointID           string `json:",omitempty"`
	DriverOpts           map[string]string
	MacAddressConfigured string `json:",omitempty"`
}

func toInspectView(c engine.Container, raw json.RawMessage) inspectView {
	v := inspectView{Id: c.ID, Name: "/" + c.ID, Created: c.Configuration.CreationDate.UTC().Format(time.RFC3339Nano), Image: c.Configuration.Image.Reference, Runtime: raw}
	v.Path = c.Configuration.InitProcess.Executable
	v.Args = c.Configuration.InitProcess.Arguments
	v.State.Status = containerState(c)
	v.State.Running = c.Running()
	if !c.Status.StartedDate.IsZero() {
		v.State.StartedAt = c.Status.StartedDate.UTC().Format(time.RFC3339Nano)
	}
	v.Config.Hostname = c.ID
	v.Config.Env = c.Configuration.InitProcess.Environment
	v.Config.Cmd = append([]string{c.Configuration.InitProcess.Executable}, c.Configuration.InitProcess.Arguments...)
	v.Config.Image = c.Configuration.Image.Reference
	v.Config.WorkingDir = c.Configuration.InitProcess.WorkingDirectory
	v.Config.Labels = c.Configuration.Labels
	v.Config.Tty = c.Configuration.InitProcess.Terminal
	v.HostConfig.NetworkMode = "bridge"
	if len(c.Configuration.Networks) == 0 {
		v.HostConfig.NetworkMode = "none"
	}
	v.HostConfig.ReadonlyRootfs = c.Configuration.ReadOnly
	v.HostConfig.Init = c.Configuration.UseInit
	v.HostConfig.Memory = c.Configuration.Resources.MemoryInBytes
	v.HostConfig.CpuCount = c.Configuration.Resources.CPUs
	v.HostConfig.NanoCpus = int64(c.Configuration.Resources.CPUs) * 1e9
	v.HostConfig.PortBindings = map[string][]portBinding{}
	v.NetworkSettings.Ports = map[string][]portBinding{}
	for _, p := range c.Configuration.PublishedPorts {
		key := fmt.Sprintf("%d/%s", p.ContainerPort, p.Proto)
		addr := p.HostAddress
		if addr == "" {
			addr = "0.0.0.0"
		}
		b := portBinding{HostIp: addr, HostPort: strconv.Itoa(p.HostPort)}
		v.HostConfig.PortBindings[key] = append(v.HostConfig.PortBindings[key], b)
		v.NetworkSettings.Ports[key] = append(v.NetworkSettings.Ports[key], b)
	}
	for _, m := range c.Configuration.Mounts {
		mv := mountView{RW: true}
		mv.Source, _ = m["source"].(string)
		mv.Destination, _ = m["destination"].(string)
		if t, ok := m["type"].(map[string]any); ok {
			for k := range t {
				mv.Type = map[string]string{"virtiofs": "bind", "tmpfs": "tmpfs", "block": "volume"}[k]
				if mv.Type == "" {
					mv.Type = k
				}
			}
		}
		if opts, ok := m["options"].([]any); ok {
			for _, o := range opts {
				if o == "ro" {
					mv.RW = false
				}
			}
		}
		v.Mounts = append(v.Mounts, mv)
	}
	v.NetworkSettings.Networks = map[string]networkView{}
	for _, n := range c.Status.Networks {
		prefix := 0
		if _, l, ok := strings.Cut(n.IPv4Address, "/"); ok {
			prefix, _ = strconv.Atoi(l)
		}
		v.NetworkSettings.Networks[n.Network] = networkView{IPAddress: n.IP(), Gateway: n.IPv4Gateway, MacAddress: n.MacAddress, GlobalIPv6Address: strings.SplitN(n.IPv6Address, "/", 2)[0], NetworkID: n.Network, Aliases: []string{n.Hostname}, IPPrefixLen: prefix, DriverOpts: map[string]string{}}
	}
	v.NetworkSettings.IPAddress = c.PrimaryIP()
	v.NetworkSettings.Gateway = c.Gateway()
	if len(c.Status.Networks) > 0 {
		v.NetworkSettings.MacAddress = c.Status.Networks[0].MacAddress
	}
	return v
}

// inspectContainers returns Docker-shaped views for the named containers.
func (a *App) inspectContainers(ctx context.Context, names []string) ([]any, error) {
	var items []any
	for _, name := range names {
		out, err := a.eng.Output(ctx, "inspect", name)
		if err != nil {
			return nil, err
		}
		var cs []engine.Container
		var raws []json.RawMessage
		if err := json.Unmarshal(out, &cs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(out, &raws); err != nil {
			return nil, err
		}
		if len(cs) == 0 {
			return nil, fmt.Errorf("No such object: %s", name)
		}
		items = append(items, toInspectView(cs[0], raws[0]))
	}
	return items, nil
}

// inspectAny resolves each name as a container, image, network or volume.
func (a *App) inspectAny(ctx context.Context, names []string, kind string) ([]any, error) {
	var items []any
	for _, name := range names {
		var (
			found any
			err   error
		)
		try := func(k string) bool {
			switch k {
			case "container":
				var v []any
				v, err = a.inspectContainers(ctx, []string{name})
				if err == nil {
					found = v[0]
				}
			case "image":
				var img *engine.Image
				img, err = a.eng.InspectImage(ctx, name)
				if err == nil {
					found = toImageInspect(*img)
				}
			case "network":
				var raw []map[string]any
				err = a.eng.JSON(ctx, &raw, "network", "inspect", name)
				if err == nil && len(raw) > 0 {
					found = toNetworkInspect(raw[0])
				} else if err == nil {
					err = engine.ErrNotFound
				}
			case "volume":
				var raw []map[string]any
				err = a.eng.JSON(ctx, &raw, "volume", "inspect", name)
				if err == nil && len(raw) > 0 {
					found = toVolumeInspect(raw[0])
				} else if err == nil {
					err = engine.ErrNotFound
				}
			}
			return err == nil
		}
		for _, k := range []string{"container", "image", "network", "volume"} {
			if kind != "" && kind != k {
				continue
			}
			if try(k) {
				break
			}
			if !looksMissing(err) {
				return nil, err
			}
		}
		if found == nil {
			return nil, fmt.Errorf("No such object: %s", name)
		}
		items = append(items, found)
	}
	return items, nil
}

// looksMissing reports whether a runtime error means the object does not
// exist, as opposed to a failure worth surfacing.
func looksMissing(err error) bool {
	if err == nil {
		return true
	}
	if engine.IsNotFound(err) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, m := range []string{"not found", "no such", "does not exist", "unable to", "notfound"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func (a *App) inspectCommand() *cobra.Command {
	var format, kind string
	var size bool
	cmd := &cobra.Command{
		Use:   "inspect [OPTIONS] NAME|ID [NAME|ID...]",
		Short: "Return low-level information on Docker objects",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = size
			items, err := a.inspectAny(cmd.Context(), args, kind)
			if err != nil {
				return err
			}
			return renderInspect(a.console.Out, format, items)
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "", "Format output using a custom template: 'json' or a Go template")
	cmd.Flags().StringVar(&kind, "type", "", "Return JSON for specified type (container|image|network|volume)")
	cmd.Flags().BoolVarP(&size, "size", "s", false, "Accepted for compatibility with Docker; ignored")
	_ = cmd.Flags().MarkHidden("size")
	return cmd
}

func (a *App) waitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "wait CONTAINER [CONTAINER...]",
		Short: "Block until one or more containers stop, then print their exit codes",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.warn("the container runtime does not report exit codes of detached containers; wait prints 0 once they stop")
			for _, name := range args {
				for {
					c, err := a.eng.InspectContainer(cmd.Context(), name)
					if err != nil {
						return err
					}
					if !c.Running() {
						break
					}
					select {
					case <-cmd.Context().Done():
						return cmd.Context().Err()
					case <-time.After(500 * time.Millisecond):
					}
				}
				fmt.Fprintln(a.console.Out, 0)
			}
			return nil
		},
	}
}

func (a *App) portCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "port CONTAINER [PRIVATE_PORT[/PROTO]]",
		Short: "List port mappings or a specific mapping for the container",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.eng.InspectContainer(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			want, proto := "", ""
			if len(args) == 2 {
				want, proto, _ = strings.Cut(args[1], "/")
			}
			for _, p := range c.Configuration.PublishedPorts {
				if want != "" && (strconv.Itoa(p.ContainerPort) != want || (proto != "" && proto != p.Proto)) {
					continue
				}
				addr := p.HostAddress
				if addr == "" {
					addr = "0.0.0.0"
				}
				if want != "" {
					fmt.Fprintf(a.console.Out, "%s:%d\n", addr, p.HostPort)
				} else {
					fmt.Fprintf(a.console.Out, "%d/%s -> %s:%d\n", p.ContainerPort, p.Proto, addr, p.HostPort)
				}
			}
			return nil
		},
	}
}

func (a *App) cpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cp [OPTIONS] CONTAINER:SRC_PATH DEST_PATH|-\n  cp [OPTIONS] SRC_PATH|- CONTAINER:DEST_PATH",
		Short: "Copy files/folders between a container and the local filesystem",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.ignored(cmd, "archive", "follow-link", "quiet")
			return a.runAttached(cmd.Context(), "cp", args[0], args[1])
		},
	}
	cmd.Flags().BoolP("archive", "a", false, "Accepted for compatibility with Docker; ignored")
	cmd.Flags().BoolP("follow-link", "L", false, "Accepted for compatibility with Docker; ignored")
	cmd.Flags().BoolP("quiet", "q", false, "Accepted for compatibility with Docker; ignored")
	return cmd
}

func (a *App) exportCommand() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "export [OPTIONS] CONTAINER",
		Short: "Export a container's filesystem as a tar archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eargs := []string{"export"}
			if output != "" {
				eargs = append(eargs, "--output", output)
			}
			return a.exec(append(eargs, args[0])...)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write to a file, instead of STDOUT")
	return cmd
}

func (a *App) statsCommand() *cobra.Command {
	var noStream, all, noTrunc bool
	var format string
	cmd := &cobra.Command{
		Use:   "stats [OPTIONS] [CONTAINER...]",
		Short: "Display a live stream of container(s) resource usage statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = all
			_ = noTrunc
			sargs := []string{"stats"}
			if noStream {
				sargs = append(sargs, "--no-stream")
			}
			if format == "json" {
				sargs = append(sargs, "--format", "json")
			} else if format != "" {
				a.warn("--format templates are not supported for stats; showing the runtime's table")
			}
			return a.exec(append(sargs, args...)...)
		},
	}
	cmd.Flags().BoolVar(&noStream, "no-stream", false, "Disable streaming stats and only pull the first result")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Accepted for compatibility with Docker; ignored")
	cmd.Flags().BoolVar(&noTrunc, "no-trunc", false, "Accepted for compatibility with Docker; ignored")
	cmd.Flags().StringVar(&format, "format", "", "Format output ('json' or the runtime's table)")
	return cmd
}

func (a *App) topCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "top CONTAINER [ps OPTIONS]",
		Short: "Display the running processes of a container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			psArgs := args[1:]
			if len(psArgs) == 0 {
				psArgs = []string{"aux"}
			}
			return a.exec(append([]string{"exec", args[0], "ps"}, psArgs...)...)
		},
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func (a *App) attachCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [OPTIONS] CONTAINER",
		Short: "Attach local standard input, output, and error streams to a running container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.eng.InspectContainer(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if c.Running() {
				return errors.New("the container runtime cannot attach to a running container; use `logs -f` or `exec -it`")
			}
			return a.exec("start", "--attach", "--interactive", args[0])
		},
	}
}

// containerCommand groups the `docker container ...` spellings.
func (a *App) containerCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "container", Short: "Manage containers"}
	ls := a.psCommand()
	ls.Use = "ls [OPTIONS]"
	ls.Aliases = []string{"list", "ps"}
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Remove all stopped containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			if !force && !a.confirm("WARNING! This will remove all stopped containers.\nAre you sure you want to continue?") {
				return nil
			}
			return a.runAttached(cmd.Context(), "prune")
		},
	}
	prune.Flags().BoolP("force", "f", false, "Do not prompt for confirmation")
	cmd.AddCommand(ls, a.runCommand(), a.createCommand(), a.startCommand(), a.stopCommand(), a.restartCommand(),
		a.killCommand(), a.rmCommand(), a.logsCommand(), a.execCommand(), a.waitCommand(), a.portCommand(),
		a.cpCommand(), a.exportCommand(), a.statsCommand(), a.topCommand(), a.attachCommand(), prune,
		a.containerInspectCommand(), a.cleanCommand())
	return cmd
}

// cleanCommand exposes the runtime's `clean`, which trims unused blocks
// from a container's disks to give space back to the host. Docker has no
// equivalent, so this is an apple-docker extension.
func (a *App) cleanCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "clean [CONTAINER...]",
		Short: "Reclaim disk space from containers by trimming their filesystems (apple-docker extension)",
		Long:  "Runs the runtime's `clean` on the named containers, or on every running container when none are given.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				cs, err := a.eng.ListContainers(cmd.Context(), false)
				if err != nil {
					return err
				}
				for _, c := range cs {
					// The runtime's own helpers, such as the image builder,
					// refuse trimming; only user containers are cleaned.
					if c.Label("com.apple.container.resource.role") != "" {
						continue
					}
					args = append(args, c.ID)
				}
				if len(args) == 0 {
					fmt.Fprintln(a.console.Out, "No running containers to clean")
					return nil
				}
			}
			return a.runAttached(cmd.Context(), append([]string{"clean"}, args...)...)
		},
	}
}

func (a *App) containerInspectCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "inspect [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Display detailed information on one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := a.inspectContainers(cmd.Context(), args)
			if err != nil {
				return err
			}
			return renderInspect(a.console.Out, format, items)
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "", "Format output using a custom template")
	return cmd
}
