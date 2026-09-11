package engine

import (
	"context"
	"encoding/binary"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// RuntimeDefault is the value that hands a setting back to the runtime: with
// APPLE_COMPOSE_MEMORY, APPLE_COMPOSE_CPUS or APPLE_COMPOSE_DNS set to it,
// apple-compose passes no flag and the runtime's own default applies.
const RuntimeDefault = "runtime"

// minDefaultMemory is the floor for the memory default. SQL Server, the most
// demanding common image, refuses to start below two gigabytes.
const minDefaultMemory = 2 << 30

// fallbackNameservers are used when the runtime's gateway resolver is silent
// and the Mac has no resolver a container can reach.
var fallbackNameservers = []string{"1.1.1.1", "8.8.8.8"}

// HostProbe is everything the defaults learn from the Mac, so tests can
// substitute fixed answers.
type HostProbe struct {
	// MemoryBytes is the host's physical memory.
	MemoryBytes func() uint64
	// CPUs is the host's logical CPU count.
	CPUs func() int
	// DNSAnswers reports whether a DNS server answers at addr.
	DNSAnswers func(addr string) bool
	// Resolvers lists the host's configured nameservers.
	Resolvers func() []string
}

// hostProbe returns the real host probe.
func hostProbe() HostProbe {
	return HostProbe{MemoryBytes: hostMemoryBytes, CPUs: hostCPUs, DNSAnswers: dnsAnswers, Resolvers: hostResolvers}
}

// DefaultMemory is the memory size given to a container that sets no limit.
// Docker imposes none, while the runtime gives each VM one gigabyte, too
// little for common images. A VM's memory is committed only as the guest
// touches it, so half the host's memory costs nothing until it is used.
func (e *Engine) DefaultMemory() string {
	if v, ok := os.LookupEnv(EnvDefaultMemory); ok {
		v = strings.TrimSpace(v)
		if v == RuntimeDefault {
			return ""
		}
		if v != "" {
			return v
		}
	}
	if e.NoHostDefaults {
		return ""
	}
	half := e.probe().MemoryBytes() / 2
	if half < minDefaultMemory {
		half = minDefaultMemory
	}
	return strconv.FormatUint(half>>30, 10) + "g"
}

// DefaultCPUs is the CPU count given to a container that sets no limit: every
// host CPU, as Docker allows. Idle virtual CPUs cost the host nothing.
func (e *Engine) DefaultCPUs() int {
	if v, ok := os.LookupEnv(EnvDefaultCPUs); ok {
		v = strings.TrimSpace(v)
		if v == RuntimeDefault {
			return 0
		}
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if e.NoHostDefaults {
		return 0
	}
	return e.probe().CPUs()
}

// DefaultDNS returns nameservers for a container joining the named networks
// that sets none of its own, or nil to keep the runtime's resolver.
//
// The runtime points containers at a resolver on the network's gateway. On
// some Macs nothing answers there, and every name lookup in every container
// fails. The gateway is probed once per process; when it is silent the Mac's
// own resolvers are used, or public ones when those are only reachable from
// the Mac itself.
func (e *Engine) DefaultDNS(ctx context.Context, networks ...string) []string {
	if v, ok := os.LookupEnv(EnvDefaultDNS); ok {
		v = strings.TrimSpace(v)
		if v == RuntimeDefault {
			return nil
		}
		if list := splitList(v); len(list) > 0 {
			return list
		}
	}
	if e.NoHostDefaults || e.DryRun {
		return nil
	}
	network := "default"
	for _, n := range networks {
		if n == "none" {
			return nil
		}
		if n != "" {
			network = n
			break
		}
	}
	gateway := e.gatewayOf(ctx, network)
	if gateway == "" {
		return nil
	}
	e.dnsMu.Lock()
	defer e.dnsMu.Unlock()
	if e.dnsByGateway == nil {
		e.dnsByGateway = map[string][]string{}
	}
	if servers, ok := e.dnsByGateway[gateway]; ok {
		return servers
	}
	var servers []string
	if !e.probe().DNSAnswers(net.JoinHostPort(gateway, "53")) {
		servers = reachableResolvers(e.probe().Resolvers())
		if len(servers) == 0 {
			servers = fallbackNameservers
		}
	}
	e.dnsByGateway[gateway] = servers
	return servers
}

func (e *Engine) probe() HostProbe {
	if e.Probe != nil {
		return *e.Probe
	}
	return hostProbe()
}

// gatewayOf returns a network's IPv4 gateway, or "" when it cannot be read.
func (e *Engine) gatewayOf(ctx context.Context, network string) string {
	n, err := e.InspectNetwork(ctx, network)
	if err != nil || n == nil {
		return ""
	}
	return n.Status.IPv4Gateway
}

// reachableResolvers drops nameservers a container cannot reach: loopback
// proxies and link-local addresses exist only on the Mac.
func reachableResolvers(servers []string) []string {
	var out []string
	for _, s := range servers {
		ip := net.ParseIP(s)
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.To4() == nil {
			continue
		}
		out = append(out, s)
	}
	return out
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// dnsAnswers sends one DNS query and reports whether anything replied.
func dnsAnswers(addr string) bool {
	conn, err := net.DialTimeout("udp", addr, 700*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	id := uint16(rand.Uint32())
	query := make([]byte, 12, 32)
	binary.BigEndian.PutUint16(query[0:], id)
	binary.BigEndian.PutUint16(query[2:], 0x0100) // recursion desired
	binary.BigEndian.PutUint16(query[4:], 1)      // one question
	query = append(query, "\x05apple\x03com\x00\x00\x01\x00\x01"...)
	_ = conn.SetDeadline(time.Now().Add(700 * time.Millisecond))
	if _, err := conn.Write(query); err != nil {
		return false
	}
	reply := make([]byte, 512)
	n, err := conn.Read(reply)
	return err == nil && n >= 2 && binary.BigEndian.Uint16(reply) == id
}

// hostResolvers reads the Mac's default resolver list from scutil, which
// includes resolvers that /etc/resolv.conf omits.
func hostResolvers() []string {
	out, err := exec.Command("scutil", "--dns").Output()
	if err != nil {
		return nil
	}
	return parseResolvers(string(out))
}

// parseResolvers returns the nameservers of the first resolver scutil lists,
// which is the default one.
func parseResolvers(out string) []string {
	var servers []string
	inFirst := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "resolver #1"):
			if inFirst {
				return servers
			}
			inFirst = true
		case strings.HasPrefix(line, "resolver #"):
			if inFirst {
				return servers
			}
		case inFirst && strings.HasPrefix(line, "nameserver["):
			if _, v, ok := strings.Cut(line, ":"); ok {
				servers = append(servers, strings.TrimSpace(v))
			}
		}
	}
	return servers
}
