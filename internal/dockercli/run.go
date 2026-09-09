package dockercli

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skuirrels/apple-compose/internal/engine"
)

// runOptions are Docker's `run`/`create` flags.
type runOptions struct {
	name        string
	detach      bool
	remove      bool
	interactive bool
	tty         bool
	env         []string
	envFiles    []string
	workdir     string
	user        string
	labels      []string
	publish     []string
	publishAll  bool
	volumes     []string
	mounts      []string
	tmpfs       []string
	networks    []string
	dns         []string
	dnsSearch   []string
	dnsOption   []string
	entrypoint  string
	platform    string
	cpus        string
	memory      string
	capAdd      []string
	capDrop     []string
	privileged  bool
	readOnly    bool
	init        bool
	shmSize     string
	ulimits     []string
	cidfile     string
	restart     string
	hostname    string
	pull        string
	// Docker flags with no equivalent, accepted so scripts keep working.
	ignoredStrings []string
}

// ignoredRunFlags are Docker options the runtime cannot honour.
var ignoredRunFlags = []string{
	"add-host", "cgroup-parent", "cgroupns", "cpu-period", "cpu-quota", "cpu-shares", "cpuset-cpus", "cpuset-mems",
	"device", "device-read-bps", "device-write-bps", "domainname", "expose", "gpus", "group-add", "health-cmd",
	"health-interval", "health-retries", "health-start-period", "health-timeout", "no-healthcheck", "ip", "ip6",
	"ipc", "isolation", "kernel-memory", "link", "log-driver", "log-opt", "mac-address", "memory-reservation",
	"memory-swap", "memory-swappiness", "oom-kill-disable", "oom-score-adj", "pid", "pids-limit", "runtime",
	"security-opt", "sig-proxy", "stop-signal", "stop-timeout", "storage-opt", "sysctl", "userns", "uts",
	"volume-driver", "volumes-from", "detach-keys", "annotation", "blkio-weight", "cpu-rt-period", "cpu-rt-runtime",
	"device-cgroup-rule", "disable-content-trust", "quiet",
}

func addRunFlags(f *pflag.FlagSet, o *runOptions, create bool) {
	f.StringVar(&o.name, "name", "", "Assign a name to the container")
	if !create {
		f.BoolVarP(&o.detach, "detach", "d", false, "Run container in background and print container ID")
	}
	f.BoolVar(&o.remove, "rm", false, "Automatically remove the container and its associated anonymous volumes when it exits")
	f.BoolVarP(&o.interactive, "interactive", "i", false, "Keep STDIN open even if not attached")
	f.BoolVarP(&o.tty, "tty", "t", false, "Allocate a pseudo-TTY")
	f.StringArrayVarP(&o.env, "env", "e", nil, "Set environment variables")
	f.StringArrayVar(&o.envFiles, "env-file", nil, "Read in a file of environment variables")
	f.StringVarP(&o.workdir, "workdir", "w", "", "Working directory inside the container")
	f.StringVarP(&o.user, "user", "u", "", "Username or UID (format: <name|uid>[:<group|gid>])")
	f.StringArrayVarP(&o.labels, "label", "l", nil, "Set meta data on a container")
	f.StringArrayVarP(&o.publish, "publish", "p", nil, "Publish a container's port(s) to the host")
	f.BoolVarP(&o.publishAll, "publish-all", "P", false, "Publish all exposed ports to random ports")
	f.StringArrayVarP(&o.volumes, "volume", "v", nil, "Bind mount a volume")
	f.StringArrayVar(&o.mounts, "mount", nil, "Attach a filesystem mount to the container")
	f.StringArrayVar(&o.tmpfs, "tmpfs", nil, "Mount a tmpfs directory")
	f.StringArrayVar(&o.networks, "network", nil, "Connect a container to a network")
	f.StringArrayVar(&o.networks, "net", nil, "Alias of --network")
	_ = f.MarkHidden("net")
	f.StringArrayVar(&o.dns, "dns", nil, "Set custom DNS servers")
	f.StringArrayVar(&o.dnsSearch, "dns-search", nil, "Set custom DNS search domains")
	f.StringArrayVar(&o.dnsOption, "dns-option", nil, "Set DNS options")
	f.StringVar(&o.entrypoint, "entrypoint", "", "Overwrite the default ENTRYPOINT of the image")
	f.StringVar(&o.platform, "platform", "", "Set platform if server is multi-platform capable")
	f.StringVar(&o.cpus, "cpus", "", "Number of CPUs")
	f.StringVarP(&o.memory, "memory", "m", "", "Memory limit")
	f.StringArrayVar(&o.capAdd, "cap-add", nil, "Add Linux capabilities")
	f.StringArrayVar(&o.capDrop, "cap-drop", nil, "Drop Linux capabilities")
	f.BoolVar(&o.privileged, "privileged", false, "Give extended privileges to this container")
	f.BoolVar(&o.readOnly, "read-only", false, "Mount the container's root filesystem as read only")
	f.BoolVar(&o.init, "init", false, "Run an init inside the container that forwards signals and reaps processes")
	f.StringVar(&o.shmSize, "shm-size", "", "Size of /dev/shm")
	f.StringArrayVar(&o.ulimits, "ulimit", nil, "Ulimit options")
	f.StringVar(&o.cidfile, "cidfile", "", "Write the container ID to the file")
	f.StringVar(&o.restart, "restart", "no", "Restart policy to apply when a container exits")
	// Docker gives -h to --hostname, so help keeps only its long form here.
	f.Bool("help", false, "Print usage")
	f.StringVarP(&o.hostname, "hostname", "h", "", "Container host name")
	f.StringVar(&o.pull, "pull", "missing", `Pull image before running ("always", "missing", "never")`)
	for _, n := range ignoredRunFlags {
		f.StringArray(n, nil, "Accepted for compatibility with Docker; ignored")
		_ = f.MarkHidden(n)
	}
}

// translateRun turns Docker run options into `container create/run` args.
func (a *App) translateRun(cmd *cobra.Command, o runOptions, image string, command []string, verb string) ([]string, error) {
	args := []string{verb}
	if o.name != "" {
		args = append(args, "--name", o.name)
	}
	if verb == "run" && o.detach {
		args = append(args, "--detach")
	}
	if o.remove {
		args = append(args, "--rm")
	}
	if o.interactive {
		args = append(args, "--interactive")
	}
	if o.tty {
		args = append(args, "--tty")
	}
	for _, e := range o.env {
		args = append(args, "--env", e)
	}
	for _, f := range o.envFiles {
		args = append(args, "--env-file", f)
	}
	if o.workdir != "" {
		args = append(args, "--workdir", o.workdir)
	}
	if o.user != "" {
		args = append(args, "--user", o.user)
	}
	for _, l := range o.labels {
		args = append(args, "--label", l)
	}
	for _, p := range o.publish {
		spec, err := publishSpec(p)
		if err != nil {
			return nil, err
		}
		args = append(args, "--publish", spec)
	}
	if o.publishAll {
		a.warn("--publish-all is not supported: the container runtime cannot allocate host ports; publish ports explicitly")
	}
	for _, v := range o.volumes {
		args = append(args, "--volume", v)
	}
	for _, m := range o.mounts {
		args = append(args, "--mount", m)
	}
	for _, t := range o.tmpfs {
		args = append(args, "--tmpfs", strings.SplitN(t, ":", 2)[0])
	}
	for _, n := range o.networks {
		switch n {
		case "host":
			return nil, fmt.Errorf("--network host is not possible: containers run in their own virtual machines")
		case "none":
			args = append(args, "--network", "none")
		case "bridge", "":
		default:
			if strings.HasPrefix(n, "container:") {
				return nil, fmt.Errorf("--network %s is not possible on the container runtime", n)
			}
			args = append(args, "--network", n)
		}
	}
	dns := o.dns
	if len(dns) == 0 {
		dns = engine.DefaultDNS()
	}
	for _, d := range dns {
		args = append(args, "--dns", d)
	}
	for _, d := range o.dnsSearch {
		args = append(args, "--dns-search", d)
	}
	for _, d := range o.dnsOption {
		args = append(args, "--dns-option", d)
	}
	if cmd.Flags().Changed("entrypoint") {
		args = append(args, "--entrypoint", o.entrypoint)
	}
	if o.platform != "" {
		args = append(args, "--platform", o.platform)
	}
	if o.cpus != "" {
		n, err := strconv.ParseFloat(o.cpus, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid --cpus %q", o.cpus)
		}
		whole := int(math.Ceil(n))
		if float64(whole) != n {
			a.warn("--cpus %s rounded up to %d: the container runtime allocates whole CPUs to a VM", o.cpus, whole)
		}
		args = append(args, "--cpus", strconv.Itoa(whole))
	}
	if o.memory != "" {
		args = append(args, "--memory", o.memory)
	}
	if o.privileged {
		a.warn("--privileged has no equivalent in an isolated VM; granting all capabilities instead")
		args = append(args, "--cap-add", "ALL")
	}
	for _, c := range o.capAdd {
		args = append(args, "--cap-add", c)
	}
	for _, c := range o.capDrop {
		args = append(args, "--cap-drop", c)
	}
	if o.readOnly {
		args = append(args, "--read-only")
	}
	if o.init {
		args = append(args, "--init")
	}
	if o.shmSize != "" {
		args = append(args, "--shm-size", o.shmSize)
	}
	for _, u := range o.ulimits {
		args = append(args, "--ulimit", u)
	}
	if o.cidfile != "" {
		args = append(args, "--cidfile", o.cidfile)
	}
	if o.restart != "" && o.restart != "no" {
		// apple-compose's supervisor honours the policy for detached
		// containers; it finds them through these labels.
		service := o.name
		if service == "" {
			service = strings.NewReplacer("/", "-", ":", "-", "@", "-").Replace(image)
		}
		args = append(args, restartLabels(o.restart, service)...)
		if verb == "run" && !o.detach {
			a.warn("--restart %s applies once the container runs detached; this attached session ends when it exits", o.restart)
		}
	}
	if o.hostname != "" {
		a.warn("--hostname is ignored: the container runtime derives the hostname from the container name")
	}
	a.ignored(cmd, ignoredRunFlags...)
	args = append(args, image)
	args = append(args, command...)
	return args, nil
}

// publishSpec validates Docker's -p forms. The runtime shares the syntax
// but cannot allocate ephemeral host ports.
func publishSpec(p string) (string, error) {
	spec, proto, _ := strings.Cut(p, "/")
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("-p %s: the container runtime cannot allocate a host port; use HOST:CONTAINER", p)
	}
	if proto != "" {
		return spec + "/" + proto, nil
	}
	return spec, nil
}

func (a *App) runCommand() *cobra.Command {
	var o runOptions
	cmd := &cobra.Command{
		Use:   "run [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create and run a new container from an image",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.pullPolicy(cmd.Context(), o.pull, args[0]); err != nil {
				return err
			}
			translated, err := a.translateRun(cmd, o, args[0], args[1:], "run")
			if err != nil {
				return err
			}
			if o.detach && o.restart != "" && o.restart != "no" {
				// Stay in the process so the supervisor can be started
				// once the container is running.
				if err := a.runAttached(cmd.Context(), translated...); err != nil {
					return err
				}
				a.ensureSupervisor(cmd.Context())
				return nil
			}
			return a.exec(translated...)
		},
	}
	cmd.Flags().SetInterspersed(false)
	addRunFlags(cmd.Flags(), &o, false)
	return cmd
}

func (a *App) createCommand() *cobra.Command {
	var o runOptions
	cmd := &cobra.Command{
		Use:   "create [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short: "Create a new container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.pullPolicy(cmd.Context(), o.pull, args[0]); err != nil {
				return err
			}
			translated, err := a.translateRun(cmd, o, args[0], args[1:], "create")
			if err != nil {
				return err
			}
			out, err := a.eng.Mutate(cmd.Context(), translated...)
			if err != nil {
				return err
			}
			if s := strings.TrimSpace(string(out)); s != "" {
				fmt.Fprintln(a.console.Out, s)
			}
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false)
	addRunFlags(cmd.Flags(), &o, true)
	return cmd
}

// pullPolicy applies Docker's --pull before creating a container.
func (a *App) pullPolicy(ctx context.Context, policy, image string) error {
	switch policy {
	case "always":
		return a.runAttached(ctx, "image", "pull", image)
	case "never":
		if _, err := a.eng.InspectImage(ctx, image); err != nil {
			return fmt.Errorf("image %s not found locally and --pull=never was given", image)
		}
	}
	return nil
}
