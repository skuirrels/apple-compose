package compose

import (
	"fmt"
	"maps"
	"math"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/state"
)

// ExtensionKey is the compose extension apple-compose reads for runtime
// specific tuning that has no compose equivalent.
const ExtensionKey = "x-apple-compose"

// Extension holds x-apple-compose settings for a service.
type Extension struct {
	// Args are extra arguments appended verbatim to `container create`.
	Args []string
	// HostsFile controls the generated /etc/hosts mount. Defaults to true.
	HostsFile bool
	// Rosetta forces Rosetta translation on.
	Rosetta bool
	// Kernel sets a custom kernel path.
	Kernel string
	// Virtualization exposes nested virtualisation to the container.
	Virtualization bool
}

// extensionFor decodes x-apple-compose from a service.
func extensionFor(s types.ServiceConfig) (Extension, error) {
	ext := Extension{HostsFile: true}
	raw, ok := s.Extensions[ExtensionKey]
	if !ok {
		return ext, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ext, fmt.Errorf("service %s: %s must be a mapping", s.Name, ExtensionKey)
	}
	for k, v := range m {
		switch k {
		case "args":
			list, ok := v.([]any)
			if !ok {
				return ext, fmt.Errorf("service %s: %s.args must be a list", s.Name, ExtensionKey)
			}
			for _, a := range list {
				ext.Args = append(ext.Args, fmt.Sprint(a))
			}
		case "hosts_file":
			b, ok := v.(bool)
			if !ok {
				return ext, fmt.Errorf("service %s: %s.hosts_file must be a boolean", s.Name, ExtensionKey)
			}
			ext.HostsFile = b
		case "rosetta":
			ext.Rosetta, _ = v.(bool)
		case "virtualization":
			ext.Virtualization, _ = v.(bool)
		case "kernel":
			ext.Kernel = fmt.Sprint(v)
		default:
			return ext, fmt.Errorf("service %s: unknown %s key %q", s.Name, ExtensionKey, k)
		}
	}
	return ext, nil
}

// createSpec is everything needed to build one container.
type createSpec struct {
	service   types.ServiceConfig
	name      string
	number    int
	oneOff    bool
	hash      string
	hostsPath string
	// overrides from `run`
	entrypoint *[]string
	command    []string
	env        []string
	user       string
	workdir    string
	volumes    []string
	ports      []string
	labels     map[string]string
	noPorts    bool
	tty        bool
	stdin      bool
	autoRemove bool
}

// createArgs translates a service into `container create` arguments.
func (r *Runner) createArgs(spec createSpec) ([]string, error) {
	p := r.Project
	s := spec.service
	ext, err := extensionFor(s)
	if err != nil {
		return nil, err
	}
	var args []string
	add := func(a ...string) { args = append(args, a...) }

	add("--name", spec.name)

	// Labels: user labels first, then compose labels so ours win.
	labels := map[string]string{}
	for k, v := range s.Labels {
		labels[k] = v
	}
	for k, v := range s.CustomLabels {
		labels[k] = v
	}
	labels[project.LabelContainerNumber] = strconv.Itoa(spec.number)
	labels[project.LabelConfigHash] = spec.hash
	labels[project.LabelManagedBy] = "true"
	if spec.oneOff {
		labels[project.LabelOneOff] = "True"
	}
	if deps := dependsOnLabel(s); deps != "" {
		labels[project.LabelDependsOn] = deps
	}
	if s.StopSignal != "" {
		labels[LabelStopSignal] = s.StopSignal
	}
	if s.StopGracePeriod != nil {
		labels[LabelStopGrace] = time.Duration(*s.StopGracePeriod).String()
	}
	if v := restartLabel(s.Restart); v != "" && !spec.oneOff {
		labels[LabelRestart] = v
	}
	if spec.hostsPath != "" && ext.HostsFile {
		labels[LabelHostsFile] = spec.hostsPath
	}
	for k, v := range spec.labels {
		labels[k] = v
	}
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		add("--label", k+"="+labels[k])
	}

	if s.Platform != "" {
		add("--platform", s.Platform)
	}
	if ext.Rosetta {
		add("--rosetta")
	}
	if ext.Kernel != "" {
		add("--kernel", ext.Kernel)
	}
	if ext.Virtualization {
		add("--virtualization")
	}

	// Environment: nil values are unresolved host variables, which Docker omits.
	for _, k := range slices.Sorted(maps.Keys(s.Environment)) {
		if v := s.Environment[k]; v != nil {
			add("--env", k+"="+*v)
		}
	}
	for _, kv := range spec.env {
		add("--env", kv)
	}

	workdir := s.WorkingDir
	if spec.workdir != "" {
		workdir = spec.workdir
	}
	if workdir != "" {
		add("--workdir", workdir)
	}
	user := s.User
	if spec.user != "" {
		user = spec.user
	}
	if user != "" {
		add("--user", user)
	}

	// Resources: the runtime allocates whole CPUs to the VM. Services
	// without limits get the APPLE_COMPOSE_CPUS/MEMORY defaults, if set.
	if cpus := cpusFor(s); cpus > 0 {
		add("--cpus", strconv.Itoa(cpus))
	} else if n := engine.DefaultCPUs(); n > 0 {
		add("--cpus", strconv.Itoa(n))
	}
	if mem := memoryFor(s); mem > 0 {
		add("--memory", megabytes(mem))
	} else if m := engine.DefaultMemory(); m != "" {
		add("--memory", m)
	}

	// Ports.
	if !spec.noPorts {
		for _, port := range s.Ports {
			specs, err := publishSpecs(s.Name, port)
			if err != nil {
				return nil, err
			}
			for _, ps := range specs {
				add("--publish", ps)
			}
		}
	}
	for _, ps := range spec.ports {
		add("--publish", ps)
	}

	// Mounts.
	for _, v := range s.Volumes {
		m, err := r.mountArg(s, v)
		if err != nil {
			return nil, err
		}
		add(m...)
	}
	for _, t := range s.Tmpfs {
		add("--tmpfs", t)
	}
	for _, v := range spec.volumes {
		add("--volume", v)
	}
	secretMounts, err := r.secretMounts(s)
	if err != nil {
		return nil, err
	}
	add(secretMounts...)
	if spec.hostsPath != "" && ext.HostsFile {
		add("--volume", spec.hostsPath+":/etc/hosts")
	}

	// Networks.
	netArgs, err := r.networkArgs(s)
	if err != nil {
		return nil, err
	}
	add(netArgs...)
	dns := s.DNS
	if len(dns) == 0 {
		dns = engine.DefaultDNS()
	}
	for _, d := range dns {
		add("--dns", d)
	}
	for _, d := range s.DNSSearch {
		add("--dns-search", d)
	}
	for _, d := range s.DNSOpts {
		add("--dns-option", d)
	}

	// Security and process settings.
	for _, c := range s.CapAdd {
		add("--cap-add", c)
	}
	for _, c := range s.CapDrop {
		add("--cap-drop", c)
	}
	if s.Privileged {
		r.warnOnce("privileged:"+s.Name, "service %s: privileged has no equivalent in an isolated VM; granting all capabilities instead", s.Name)
		add("--cap-add", "ALL")
	}
	if s.ReadOnly {
		add("--read-only")
	}
	if s.Init != nil && *s.Init {
		add("--init")
	}
	if s.ShmSize > 0 {
		add("--shm-size", megabytes(int64(s.ShmSize)))
	}
	for _, name := range slices.Sorted(maps.Keys(s.Ulimits)) {
		u := s.Ulimits[name]
		if u == nil {
			continue
		}
		if u.Single > 0 {
			add("--ulimit", fmt.Sprintf("%s=%d", name, u.Single))
		} else {
			add("--ulimit", fmt.Sprintf("%s=%d:%d", name, u.Soft, u.Hard))
		}
	}
	tty := s.Tty || spec.tty
	if tty {
		add("--tty")
	}
	if spec.stdin {
		add("--interactive")
	}
	if spec.autoRemove {
		add("--rm")
	}
	r.warnUnsupported(s)
	add(ext.Args...)

	// Image, entrypoint and command.
	entrypoint := s.Entrypoint
	if spec.entrypoint != nil {
		entrypoint = *spec.entrypoint
	}
	command := s.Command
	if spec.command != nil {
		command = spec.command
	}
	cmdArgs, err := processArgs(entrypoint, command, spec.entrypoint != nil || s.Entrypoint != nil)
	if err != nil {
		return nil, fmt.Errorf("service %s: %w", s.Name, err)
	}
	add(project.ImageName(p, s))
	add(cmdArgs...)
	return args, nil
}

// processArgs maps compose entrypoint/command onto the runtime's flags, which
// follow Docker's rules: an explicit entrypoint discards the image CMD, and
// the runtime cannot express an empty entrypoint override, so an explicitly
// empty entrypoint promotes the first command word instead.
func processArgs(entrypoint, command []string, entrypointSet bool) ([]string, error) {
	var args []string
	switch {
	case len(entrypoint) > 0:
		args = append(args, "--entrypoint", entrypoint[0])
		args = append(args, entrypoint[1:]...)
		args = append(args, command...)
	case entrypointSet && entrypoint != nil && len(entrypoint) == 0:
		// entrypoint: [] clears the image entrypoint.
		if len(command) == 0 {
			return nil, fmt.Errorf("entrypoint is cleared but no command is set")
		}
		args = append(args, "--entrypoint", command[0])
		args = append(args, command[1:]...)
	default:
		args = append(args, command...)
	}
	return args, nil
}

func dependsOnLabel(s types.ServiceConfig) string {
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(s.DependsOn)) {
		d := s.DependsOn[name]
		parts = append(parts, fmt.Sprintf("%s:%s:%t", name, d.Condition, d.Required))
	}
	return strings.Join(parts, ",")
}

// cpusFor returns the whole number of CPUs a service asks for, rounding up.
func cpusFor(s types.ServiceConfig) int {
	var cpus float64
	if s.CPUS > 0 {
		cpus = float64(s.CPUS)
	}
	if s.Deploy != nil && s.Deploy.Resources.Limits != nil && s.Deploy.Resources.Limits.NanoCPUs > 0 {
		cpus = float64(s.Deploy.Resources.Limits.NanoCPUs)
	}
	if s.CPUCount > 0 {
		cpus = float64(s.CPUCount)
	}
	if cpus <= 0 {
		return 0
	}
	return int(math.Ceil(cpus))
}

// memoryFor returns the memory limit in bytes, or 0 when unset.
func memoryFor(s types.ServiceConfig) int64 {
	var mem int64
	if s.MemLimit > 0 {
		mem = int64(s.MemLimit)
	}
	if s.Deploy != nil && s.Deploy.Resources.Limits != nil && s.Deploy.Resources.Limits.MemoryBytes > 0 {
		mem = int64(s.Deploy.Resources.Limits.MemoryBytes)
	}
	return mem
}

// megabytes renders bytes as the runtime's "<n>M" form, rounding up.
func megabytes(b int64) string {
	const mib = 1024 * 1024
	n := (b + mib - 1) / mib
	if n < 1 {
		n = 1
	}
	return strconv.FormatInt(n, 10) + "M"
}

// publishSpecs expands one compose port entry, including ranges, into
// runtime publish specs.
func publishSpecs(service string, p types.ServicePortConfig) ([]string, error) {
	if p.Published == "" {
		return nil, fmt.Errorf("service %s: port %d has no published host port; the container runtime cannot assign ephemeral ports, so set `published`", service, p.Target)
	}
	proto := strings.ToLower(p.Protocol)
	if proto == "" {
		proto = "tcp"
	}
	hostIP := p.HostIP
	lo, hi, err := parseRange(p.Published)
	if err != nil {
		return nil, fmt.Errorf("service %s: invalid published port %q", service, p.Published)
	}
	if hi > lo {
		// compose-go expands equal-length ranges itself, so a range left
		// here maps one container port to any free host port in it, as
		// Docker does.
		port, err := freePort(hostIP, lo, hi)
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", service, err)
		}
		lo, hi = port, port
	}
	var out []string
	for i := lo; i <= hi; i++ {
		spec := fmt.Sprintf("%d:%d/%s", i, p.Target, proto)
		if hostIP != "" {
			spec = hostIP + ":" + spec
		}
		out = append(out, spec)
	}
	return out, nil
}

// freePort returns the first host port in [lo, hi] that is not listening.
func freePort(hostIP string, lo, hi int) (int, error) {
	if hostIP == "" {
		hostIP = "0.0.0.0"
	}
	for port := lo; port <= hi; port++ {
		l, err := net.Listen("tcp", net.JoinHostPort(hostIP, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		l.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no free host port in %d-%d", lo, hi)
}

func parseRange(v string) (int, int, error) {
	lo, hi, found := strings.Cut(v, "-")
	a, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, err
	}
	if !found {
		return a, a, nil
	}
	b, err := strconv.Atoi(hi)
	if err != nil {
		return 0, 0, err
	}
	if b < a {
		return 0, 0, fmt.Errorf("range end before start")
	}
	return a, b, nil
}

// mountArg renders one service volume as runtime mount arguments.
func (r *Runner) mountArg(s types.ServiceConfig, v types.ServiceVolumeConfig) ([]string, error) {
	switch v.Type {
	case "bind":
		src := v.Source
		if !filepath.IsAbs(src) {
			src = filepath.Join(r.Project.WorkingDir, src)
		}
		create := true
		if v.Bind != nil {
			create = bool(v.Bind.CreateHostPath)
		}
		if _, err := os.Stat(src); err != nil {
			if !create {
				return nil, fmt.Errorf("service %s: bind source %s does not exist", s.Name, src)
			}
			if err := os.MkdirAll(src, 0o755); err != nil {
				return nil, fmt.Errorf("service %s: cannot create bind source %s: %w", s.Name, src, err)
			}
		}
		return bindArgs(src, v.Target, v.ReadOnly), nil
	case "volume":
		if v.Source == "" {
			return []string{"--volume", v.Target}, nil
		}
		if dir, ok, err := r.bindBackedVolume(v.Source); err != nil {
			return nil, fmt.Errorf("service %s: %w", s.Name, err)
		} else if ok {
			return []string{"--mount", mountSpec("bind", dir, v.Target, v.ReadOnly)}, nil
		}
		name, err := project.VolumeName(r.Project, v.Source)
		if err != nil {
			// A volume not declared at the top level is used by name directly.
			name = v.Source
		}
		return []string{"--mount", mountSpec("volume", name, v.Target, v.ReadOnly)}, nil
	case "tmpfs":
		spec := v.Target
		var opts []string
		if v.Tmpfs != nil && v.Tmpfs.Size > 0 {
			opts = append(opts, "size="+megabytes(int64(v.Tmpfs.Size)))
		}
		if v.Tmpfs != nil && v.Tmpfs.Mode > 0 {
			opts = append(opts, fmt.Sprintf("mode=%o", v.Tmpfs.Mode))
		}
		if len(opts) > 0 {
			spec += ":" + strings.Join(opts, ",")
		}
		return []string{"--tmpfs", spec}, nil
	default:
		return nil, fmt.Errorf("service %s: volume type %q is not supported by the container runtime", s.Name, v.Type)
	}
}

// mountSpec renders a --mount value, falling back to -v syntax cannot be used
// because paths with commas would break the key=value parser; such paths are
// rejected instead.
func mountSpec(kind, source, target string, readOnly bool) string {
	parts := []string{"type=" + kind, "source=" + source, "target=" + target}
	if readOnly {
		parts = append(parts, "readonly")
	}
	return strings.Join(parts, ",")
}

// secretMounts renders secrets and configs as read-only file bind mounts,
// which is how Docker delivers file-backed secrets to non-swarm containers.
func (r *Runner) secretMounts(s types.ServiceConfig) ([]string, error) {
	var args []string
	for _, ref := range s.Secrets {
		src, err := r.fileObjectSource(types.FileObjectConfig(r.Project.Secrets[ref.Source]), ref.Source, "secret")
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", s.Name, err)
		}
		target := ref.Target
		if target == "" {
			target = "/run/secrets/" + ref.Source
		} else if !strings.HasPrefix(target, "/") {
			target = "/run/secrets/" + target
		}
		args = append(args, bindArgs(src, target, true)...)
	}
	for _, ref := range s.Configs {
		src, err := r.fileObjectSource(types.FileObjectConfig(r.Project.Configs[ref.Source]), ref.Source, "config")
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", s.Name, err)
		}
		target := ref.Target
		if target == "" {
			target = "/" + ref.Source
		}
		args = append(args, bindArgs(src, target, true)...)
	}
	return args, nil
}

// bindArgs renders a bind mount. The runtime's --mount parser only accepts
// directories, so single files go through -v, which also handles them.
func bindArgs(source, target string, readOnly bool) []string {
	if st, err := os.Stat(source); err == nil && !st.IsDir() {
		spec := source + ":" + target
		if readOnly {
			spec += ":ro"
		}
		return []string{"--volume", spec}
	}
	return []string{"--mount", mountSpec("bind", source, target, readOnly)}
}

// fileObjectSource resolves a secret or config to a host file, materialising
// environment and inline content into the project's private state directory.
func (r *Runner) fileObjectSource(obj types.FileObjectConfig, name, kind string) (string, error) {
	switch {
	case obj.File != "":
		if !filepath.IsAbs(obj.File) {
			return filepath.Join(r.Project.WorkingDir, obj.File), nil
		}
		return obj.File, nil
	case obj.Environment != "":
		v, ok := r.Project.Environment[obj.Environment]
		if !ok {
			v, ok = os.LookupEnv(obj.Environment)
		}
		if !ok {
			return "", fmt.Errorf("%s %q: environment variable %s is not set", kind, name, obj.Environment)
		}
		return r.writeStateFile(kind, name, v)
	case obj.Content != "":
		return r.writeStateFile(kind, name, obj.Content)
	case bool(obj.External):
		return "", fmt.Errorf("%s %q: external secrets are not supported without a secret store", kind, name)
	default:
		return "", fmt.Errorf("%s %q is not defined with a file, environment or content", kind, name)
	}
}

func (r *Runner) writeStateFile(kind, name, content string) (string, error) {
	dir, err := state.SecretsDir(r.Project.Name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, kind+"-"+name)
	if r.Engine.DryRun {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// networkArgs renders the --network flags for a service.
func (r *Runner) networkArgs(s types.ServiceConfig) ([]string, error) {
	var args []string
	switch mode := s.NetworkMode; {
	case mode == "":
	case mode == "none":
		return []string{"--network", "none"}, nil
	case mode == "host":
		return nil, fmt.Errorf("service %s: network_mode: host is not supported; containers run in their own VM, so publish ports instead", s.Name)
	case strings.HasPrefix(mode, "service:") || strings.HasPrefix(mode, "container:"):
		return nil, fmt.Errorf("service %s: network_mode %q is not supported by the container runtime", s.Name, mode)
	case mode == "bridge" || mode == "default":
		return []string{"--network", "default"}, nil
	default:
		return []string{"--network", mode}, nil
	}
	for _, key := range s.NetworksByPriority() {
		name, err := project.NetworkName(r.Project, key)
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", s.Name, err)
		}
		spec := name
		mac := s.MacAddress
		if cfg := s.Networks[key]; cfg != nil && cfg.MacAddress != "" {
			mac = cfg.MacAddress
		}
		if mac != "" {
			spec += ",mac=" + mac
		}
		args = append(args, "--network", spec)
	}
	if len(args) == 0 {
		name, err := project.NetworkName(r.Project, "default")
		if err != nil {
			name = r.Project.Name + "_default"
		}
		args = append(args, "--network", name)
	}
	return args, nil
}

// warnUnsupported reports compose attributes the runtime cannot honour.
func (r *Runner) warnUnsupported(s types.ServiceConfig) {
	note := func(attr string) {
		r.warnOnce(attr+":"+s.Name, "service %s: `%s` is not supported by the container runtime and is ignored", s.Name, attr)
	}
	if len(s.Devices) > 0 {
		note("devices")
	}
	if len(s.Sysctls) > 0 {
		note("sysctls")
	}
	if len(s.SecurityOpt) > 0 {
		note("security_opt")
	}
	if s.Pid != "" {
		note("pid")
	}
	if s.Ipc != "" {
		note("ipc")
	}
	if s.UserNSMode != "" {
		note("userns_mode")
	}
	if s.CgroupParent != "" || s.Cgroup != "" {
		note("cgroup")
	}
	if len(s.GroupAdd) > 0 {
		note("group_add")
	}
	if len(s.VolumesFrom) > 0 {
		note("volumes_from")
	}
	if len(s.Gpus) > 0 {
		note("gpus")
	}
	if s.PidsLimit > 0 {
		note("pids_limit")
	}
	if s.CPUSet != "" {
		note("cpuset")
	}
	if s.MemSwapLimit > 0 {
		note("memswap_limit")
	}
	if s.Hostname != "" {
		r.warnOnce("hostname:"+s.Name, "service %s: the container runtime derives the hostname from the container name; `hostname` is added to the container's hosts file as an alias", s.Name)
	}
	if s.DomainName != "" {
		note("domainname")
	}
	if s.Runtime != "" {
		note("runtime")
	}
	if len(s.PreStart)+len(s.PostStart)+len(s.PreStop) > 0 {
		note("lifecycle hooks")
	}
}

// bindBackedVolume reports whether a top-level volume is backed by a host
// directory rather than a runtime disk image. Two spellings are honoured:
// Docker's local-driver bind options (`driver_opts: {type: none, o: bind,
// device: /path}`) and the `x-apple-compose: {shared: true}` extension,
// which keeps the directory under apple-compose's state directory. Disk
// image volumes attach to a single running container, so this is how
// several services share data.
func (r *Runner) bindBackedVolume(key string) (string, bool, error) {
	v, ok := r.Project.Volumes[key]
	if !ok {
		return "", false, nil
	}
	if v.DriverOpts["type"] == "none" && strings.Contains(v.DriverOpts["o"], "bind") {
		dev := v.DriverOpts["device"]
		if dev == "" {
			return "", false, fmt.Errorf("volume %s: driver_opts.device is required for a bind-backed volume", key)
		}
		if !filepath.IsAbs(dev) {
			dev = filepath.Join(r.Project.WorkingDir, dev)
		}
		if err := os.MkdirAll(dev, 0o755); err != nil {
			return "", false, err
		}
		return dev, true, nil
	}
	if ext, ok := v.Extensions[ExtensionKey].(map[string]any); ok {
		if shared, _ := ext["shared"].(bool); shared {
			name, err := project.VolumeName(r.Project, key)
			if err != nil {
				return "", false, err
			}
			dir, err := state.SharedVolumeDir(name)
			if err != nil {
				return "", false, err
			}
			return dir, true, nil
		}
	}
	return "", false, nil
}

// warnSharedVolumes flags named volumes mounted by more than one container,
// which the runtime can only attach to one running container at a time.
func (r *Runner) warnSharedVolumes() {
	users := map[string][]string{}
	for _, name := range r.Project.ServiceNames() {
		s, _ := r.Project.GetService(name)
		for _, v := range s.Volumes {
			if v.Type != "volume" || v.Source == "" {
				continue
			}
			if _, bind, _ := r.bindBackedVolume(v.Source); bind {
				continue
			}
			for i := 0; i < s.GetScale(); i++ {
				users[v.Source] = append(users[v.Source], name)
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(users)) {
		if len(users[key]) > 1 {
			r.warnOnce("sharedvol:"+key, "volume %s is mounted by %s; the container runtime attaches a named volume to one running container at a time. Use a bind mount, or mark it shared with `x-apple-compose: {shared: true}` on the volume", key, strings.Join(users[key], ", "))
		}
	}
}
