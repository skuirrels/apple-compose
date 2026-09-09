package dockercli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skuirrels/apple-compose/internal/compose"
	"github.com/skuirrels/apple-compose/internal/hosts"
	"github.com/skuirrels/apple-compose/internal/state"
)

// hostsFileFor prepares the generated /etc/hosts a container needs and
// returns its path with the labels describing the names it answers to.
// Docker resolves container names only on user-defined networks, so that is
// where the file is mounted; --add-host earns one on any network.
func (a *App) hostsFileFor(o runOptions) (string, map[string]string, error) {
	extra, err := parseAddHosts(o.addHosts)
	if err != nil {
		return "", nil, err
	}
	nets := userNetworks(o.networks)
	if !needsHostsFile(o) {
		if len(o.netAliases) > 0 {
			a.warn("--network-alias is ignored outside a user-defined network, as it is on Docker")
		}
		return "", nil, nil
	}
	name := o.name
	if name == "" {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return "", nil, err
		}
		name = "run-" + hex.EncodeToString(buf)
	}
	path, err := state.HostsPath(supervisedProject, name)
	if err != nil {
		return "", nil, err
	}
	if !a.eng.DryRun {
		if err := hosts.Ensure(path); err != nil {
			return "", nil, err
		}
	}
	names := compose.HostNames{ExtraHosts: extra}
	if len(o.netAliases) > 0 {
		names.Aliases = map[string][]string{}
		for _, n := range nets {
			names.Aliases[n] = o.netAliases
		}
	}
	labels := compose.HostsLabels(names)
	labels[compose.LabelHostsFile] = path
	return path, labels, nil
}

// needsHostsFile reports whether a container gets a generated hosts file. It
// answers without creating one, for callers deciding how to start it.
func needsHostsFile(o runOptions) bool {
	return len(userNetworks(o.networks)) > 0 || len(o.addHosts) > 0
}

// userNetworks keeps the networks the runtime treats as user-defined, which
// are the ones names resolve on.
func userNetworks(nets []string) []string {
	var out []string
	for _, n := range nets {
		if n == "" || n == "bridge" || n == "none" || n == "host" || strings.HasPrefix(n, "container:") {
			continue
		}
		out = append(out, n)
	}
	return out
}

// parseAddHosts reads Docker's --add-host pairs. The host-gateway sentinel is
// kept as written; the renderer swaps in the container's own gateway.
func parseAddHosts(specs []string) (map[string][]string, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := map[string][]string{}
	for _, s := range specs {
		host, addr, ok := strings.Cut(s, ":")
		if !ok {
			host, addr, ok = strings.Cut(s, "=")
		}
		if !ok || host == "" || addr == "" {
			return nil, fmt.Errorf("--add-host %s: expected HOST:IP", s)
		}
		out[host] = append(out[host], addr)
	}
	return out, nil
}

// refreshHosts rewrites every generated hosts file from the addresses the
// runtime reports now, so containers learn their neighbours as those come and
// go. Name resolution is a convenience, so failures are reported, not fatal.
func (a *App) refreshHosts(ctx context.Context) {
	if a.eng == nil || a.eng.DryRun {
		return
	}
	if err := a.supervisorRunner().RefreshHostsAll(ctx); err != nil {
		a.warn("hosts files: %v", err)
	}
}

// refreshWhenRunning waits for a container to come up before refreshing, for
// the attached path where the container starts after the call returns.
func (a *App) refreshWhenRunning(ctx context.Context, id string) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
		c, err := a.eng.InspectContainer(ctx, id)
		if err != nil || !c.Running() {
			continue
		}
		a.refreshHosts(ctx)
		return
	}
}

// pruneHostsFiles deletes the hosts files of containers that no longer exist.
func (a *App) pruneHostsFiles(ctx context.Context) {
	if a.eng == nil || a.eng.DryRun {
		return
	}
	dir, err := state.HostsDir(supervisedProject)
	if err != nil {
		return
	}
	cs, err := a.eng.ListContainers(ctx, true)
	if err != nil {
		return
	}
	keep := map[string]bool{}
	for _, c := range cs {
		if p := c.Label(compose.LabelHostsFile); p != "" {
			keep[p] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if path := filepath.Join(dir, e.Name()); !keep[path] {
			_ = os.Remove(path)
		}
	}
}

// hasHostsFile reports whether a container has a generated hosts file, and so
// whether its neighbours need telling when it starts.
func (a *App) hasHostsFile(ctx context.Context, id string) bool {
	c, err := a.eng.InspectContainer(ctx, id)
	return err == nil && c.Label(compose.LabelHostsFile) != ""
}
