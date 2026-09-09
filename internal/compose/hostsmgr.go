package compose

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/hosts"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/state"
)

// Labels carrying the name data of a container no compose project describes,
// such as one `apple-docker run` created. They let a hosts file be rebuilt
// from the runtime's own record.
const (
	LabelHostname   = "com.apple-compose.hostname"
	LabelNetAliases = "com.apple-compose.net-aliases"
	LabelExtraHosts = "com.apple-compose.extra-hosts"
	LabelLinks      = "com.apple-compose.links"
	// LabelNamesRecorded marks a container whose name data is complete in
	// its labels. Containers created before those labels existed carry no
	// marker, and only the project that owns them may rewrite their file.
	LabelNamesRecorded = "com.apple-compose.names"
)

var hostsMu sync.Mutex

// hostsPathFor returns the hosts file path for a container, creating the
// file so that it exists before the bind mount is set up.
func (r *Runner) hostsPathFor(name string) (string, error) {
	path, err := state.HostsPath(r.Project.Name, name)
	if err != nil {
		return "", err
	}
	if r.Engine.DryRun {
		return path, nil
	}
	return path, hosts.Ensure(path)
}

// RefreshHosts rewrites the hosts file of every container in the project from
// the addresses the runtime currently reports. It is called after any
// container starts, so peers learn new addresses immediately. Every container
// the runtime knows about is a candidate peer, not just the project's own:
// on a shared network Docker resolves across projects too. Containers named
// in extra are included even when the runtime's listing omits them, which is
// the case for containers created but not yet started.
func (r *Runner) RefreshHosts(ctx context.Context, extra ...string) error {
	return r.refreshHosts(ctx, false, extra...)
}

// RefreshHostsAll rewrites the hosts file of every container that has one,
// whatever created it. apple-docker uses it: a container it starts or stops
// changes the names its compose neighbours resolve too.
func (r *Runner) RefreshHostsAll(ctx context.Context) error {
	return r.refreshHosts(ctx, true)
}

func (r *Runner) refreshHosts(ctx context.Context, everything bool, extra ...string) error {
	if r.Engine.DryRun {
		return nil
	}
	hostsMu.Lock()
	defer hostsMu.Unlock()
	peers, err := r.Engine.ListContainers(ctx, true)
	if err != nil {
		return err
	}
	listed := map[string]bool{}
	for _, c := range peers {
		listed[c.ID] = true
	}
	for _, name := range extra {
		if listed[name] {
			continue
		}
		c, err := r.Engine.InspectContainer(ctx, name)
		if err != nil {
			return err
		}
		peers = append(peers, *c)
	}
	var write []engine.Container
	for _, c := range peers {
		if c.Label(project.LabelProject) == r.Project.Name || (everything && c.Label(LabelNamesRecorded) == "1") {
			write = append(write, c)
		}
	}
	return r.writeHostsFiles(write, peers)
}

// HostNames is the name data a hosts file needs beyond what the runtime
// reports: the extra names a container answers to and the fixed addresses it
// asked for.
type HostNames struct {
	Hostname   string
	Aliases    map[string][]string // network name -> aliases on that network
	ExtraHosts map[string][]string // host name -> addresses
	Links      map[string][]string // peer service -> aliases this container uses
}

// HostsLabels renders name data as container labels, so a hosts file can be
// rebuilt by a process that does not have the compose project to hand.
func HostsLabels(n HostNames) map[string]string {
	out := map[string]string{LabelNamesRecorded: "1"}
	if n.Hostname != "" {
		out[LabelHostname] = n.Hostname
	}
	for label, m := range map[string]map[string][]string{
		LabelNetAliases: n.Aliases,
		LabelExtraHosts: n.ExtraHosts,
		LabelLinks:      n.Links,
	} {
		if len(m) == 0 {
			continue
		}
		if b, err := json.Marshal(m); err == nil {
			out[label] = string(b)
		}
	}
	return out
}

// extrasFor takes a container's name data from its compose service when this
// project describes it, and from its labels otherwise.
func (r *Runner) extrasFor(c engine.Container) HostNames {
	if c.Label(project.LabelProject) == r.Project.Name {
		if s, ok := r.Project.Services[c.Label(project.LabelService)]; ok {
			return r.serviceExtras(s)
		}
	}
	n := HostNames{Hostname: c.Label(LabelHostname)}
	for v, dst := range map[string]*map[string][]string{
		c.Label(LabelNetAliases): &n.Aliases,
		c.Label(LabelExtraHosts): &n.ExtraHosts,
		c.Label(LabelLinks):      &n.Links,
	} {
		if v != "" {
			_ = json.Unmarshal([]byte(v), dst)
		}
	}
	return n
}

// serviceExtras reads name data out of a compose service, resolving network
// keys to the names the runtime knows them by.
func (r *Runner) serviceExtras(s types.ServiceConfig) HostNames {
	e := HostNames{Hostname: s.Hostname, ExtraHosts: s.ExtraHosts}
	for key, cfg := range s.Networks {
		if cfg == nil || len(cfg.Aliases) == 0 {
			continue
		}
		name, err := project.NetworkName(r.Project, key)
		if err != nil {
			name = key
		}
		if e.Aliases == nil {
			e.Aliases = map[string][]string{}
		}
		e.Aliases[name] = append(e.Aliases[name], cfg.Aliases...)
	}
	for _, l := range s.Links {
		svc, alias, found := strings.Cut(l, ":")
		if !found || alias == "" {
			continue
		}
		if e.Links == nil {
			e.Links = map[string][]string{}
		}
		e.Links[svc] = append(e.Links[svc], alias)
	}
	return e
}

// writeHostsFiles renders a hosts file for every container in write that asked
// for one, resolving the peers it shares a network with out of peers.
func (r *Runner) writeHostsFiles(write, peers []engine.Container) error {
	// Index running peers by service. Containers created outside compose
	// carry no service label and group under the empty name, which is
	// skipped when the names are rendered.
	byService := map[string][]engine.Container{}
	for _, c := range peers {
		if !c.Running() || c.Label(project.LabelOneOff) == "True" {
			continue
		}
		svc := c.Label(project.LabelService)
		byService[svc] = append(byService[svc], c)
	}
	cache := map[string]HostNames{}
	extras := func(c engine.Container) HostNames {
		e, ok := cache[c.ID]
		if !ok {
			e = r.extrasFor(c)
			cache[c.ID] = e
		}
		return e
	}
	for _, c := range write {
		path := c.Label(LabelHostsFile)
		if path == "" {
			continue
		}
		f := hosts.New()
		self := extras(c)
		// Until the container has an address, its own name resolves to a
		// loopback alias so software that looks itself up at boot works.
		if !c.Running() {
			f.Add("127.0.1.1", hostnameOf(c.ID), c.ID)
		}
		if c.Running() {
			ip := c.PrimaryIP()
			f.Add(ip, hostnameOf(c.ID), c.ID)
			// One-off containers answer to their own name only; the
			// service name keeps pointing at the service's containers.
			if c.Label(project.LabelOneOff) != "True" {
				f.Add(ip, c.Label(project.LabelService))
				f.Add(ip, self.Hostname)
			}
		}
		// Peers reachable on a shared network, in the order of this
		// container's own networks.
		for _, svc := range slices.Sorted(maps.Keys(byService)) {
			for _, peer := range byService[svc] {
				if peer.ID == c.ID {
					continue
				}
				ip := sharedIP(c, peer)
				if ip == "" {
					continue
				}
				pe := extras(peer)
				names := []string{svc, peer.ID, hostnameOf(peer.ID), pe.Hostname}
				names = append(names, aliasesOn(pe, c)...)
				names = append(names, self.Links[svc]...)
				f.Add(ip, names...)
			}
		}
		gateway := c.Gateway()
		if gateway != "" {
			f.Add(gateway, hosts.HostAliases...)
		}
		for _, host := range slices.Sorted(maps.Keys(self.ExtraHosts)) {
			for _, addr := range self.ExtraHosts[host] {
				if addr == hosts.HostGateway {
					addr = gateway
				}
				f.Add(addr, host)
			}
		}
		if err := f.Write(path); err != nil {
			return err
		}
	}
	return nil
}

// hostnameOf mirrors the runtime: the hostname is the container id up to the
// first dot.
func hostnameOf(id string) string {
	h, _, _ := strings.Cut(id, ".")
	return h
}

// sharedIP returns the peer's address on the first of c's networks that the
// peer is also attached to.
func sharedIP(c, peer engine.Container) string {
	for _, n := range networksOf(c) {
		if a := peer.Attachment(n); a != nil {
			return a.IP()
		}
	}
	return ""
}

// networksOf lists a container's networks in priority order: the live
// attachments when running, otherwise the attachments it was created with,
// so a container's hosts file can be filled in before it starts.
func networksOf(c engine.Container) []string {
	var out []string
	if c.Running() {
		for _, n := range c.Status.Networks {
			out = append(out, n.Network)
		}
		return out
	}
	for _, n := range c.Configuration.Networks {
		out = append(out, n.Network)
	}
	return out
}

// aliasesOn returns a peer's aliases on the networks the target container
// shares with it, in a stable order.
func aliasesOn(peer HostNames, target engine.Container) []string {
	var out []string
	nets := networksOf(target)
	for _, name := range slices.Sorted(maps.Keys(peer.Aliases)) {
		if slices.Contains(nets, name) {
			out = append(out, peer.Aliases[name]...)
		}
	}
	return out
}
