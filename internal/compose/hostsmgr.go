package compose

import (
	"context"
	"strings"
	"sync"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/hosts"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/state"
)

var hostsMu sync.Mutex

// hostsPathFor returns the hosts file path for a container, creating the
// file so that it exists before the bind mount is set up.
func (r *Runner) hostsPathFor(name string) (string, error) {
	path, err := state.HostsPath(r.Project.Name, name)
	if err != nil {
		return "", err
	}
	return path, hosts.Ensure(path)
}

// RefreshHosts rewrites the hosts file of every container in the project from
// the addresses the runtime currently reports. It is called after any
// container starts, so peers learn new addresses immediately.
func (r *Runner) RefreshHosts(ctx context.Context) error {
	hostsMu.Lock()
	defer hostsMu.Unlock()
	cs, err := r.containers(ctx, true)
	if err != nil {
		return err
	}
	return r.writeHostsFiles(cs)
}

func (r *Runner) writeHostsFiles(cs []engine.Container) error {
	// Index running containers by service.
	byService := map[string][]engine.Container{}
	for _, c := range cs {
		if !c.Running() || c.Label(project.LabelOneOff) == "True" {
			continue
		}
		svc := c.Label(project.LabelService)
		byService[svc] = append(byService[svc], c)
	}
	for _, c := range cs {
		path := c.Label(LabelHostsFile)
		if path == "" {
			continue
		}
		f := hosts.New()
		self, _ := r.Project.GetService(c.Label(project.LabelService))
		if c.Running() {
			ip := c.PrimaryIP()
			f.Add(ip, hostnameOf(c.ID), c.ID)
			// One-off containers answer to their own name only; the
			// service name keeps pointing at the service's containers.
			if c.Label(project.LabelOneOff) != "True" {
				f.Add(ip, c.Label(project.LabelService))
				if self.Hostname != "" {
					f.Add(ip, self.Hostname)
				}
			}
		}
		// Peers reachable on a shared network, in the order of this
		// container's own networks.
		for _, svc := range sortedKeys(byService) {
			for _, peer := range byService[svc] {
				if peer.ID == c.ID {
					continue
				}
				ip := sharedIP(c, peer)
				if ip == "" {
					continue
				}
				peerSvc, _ := r.Project.GetService(svc)
				names := []string{svc, peer.ID, hostnameOf(peer.ID)}
				if peerSvc.Hostname != "" {
					names = append(names, peerSvc.Hostname)
				}
				names = append(names, aliasesFor(peerSvc, c)...)
				names = append(names, linkAliases(self, svc)...)
				f.Add(ip, names...)
			}
		}
		gateway := c.Gateway()
		if gateway != "" {
			f.Add(gateway, hosts.HostAliases...)
		}
		for _, host := range sortedKeys(self.ExtraHosts) {
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
	for _, n := range c.Status.Networks {
		if a := peer.Attachment(n.Network); a != nil {
			return a.IP()
		}
	}
	return ""
}

// aliasesFor returns the network aliases of a peer service on networks the
// target container shares.
func aliasesFor(peer types.ServiceConfig, target engine.Container) []string {
	var out []string
	for key, cfg := range peer.Networks {
		if cfg == nil || len(cfg.Aliases) == 0 {
			continue
		}
		name := key
		if n, ok := peer.Networks[key]; ok && n != nil {
			_ = n
		}
		for _, att := range target.Status.Networks {
			if att.Network == name || strings.HasSuffix(att.Network, "_"+key) {
				out = append(out, cfg.Aliases...)
				break
			}
		}
	}
	return out
}

// linkAliases returns the aliases a service declares for a peer via links.
func linkAliases(s types.ServiceConfig, peer string) []string {
	var out []string
	for _, l := range s.Links {
		svc, alias, found := strings.Cut(l, ":")
		if svc != peer {
			continue
		}
		if found && alias != "" {
			out = append(out, alias)
		}
	}
	return out
}
