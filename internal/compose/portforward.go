package compose

import (
	"io"
	"maps"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
)

// EnvIPv6Ports names the variable that turns IPv6 port forwarding off when
// set to "off".
const EnvIPv6Ports = "APPLE_COMPOSE_IPV6_PORTS"

// ipv6PortsEnabled reports whether published ports are also offered on IPv6.
func ipv6PortsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvIPv6Ports))) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}

// forwardablePorts lists the host TCP ports that running containers publish
// on every IPv4 address. Docker publishes those on IPv6 as well, while the
// runtime listens on IPv4 only, so a client that resolves localhost to ::1 is
// refused unless something answers there.
func forwardablePorts(cs []engine.Container) []int {
	seen := map[int]bool{}
	for _, c := range cs {
		if !c.Running() {
			continue
		}
		for _, p := range c.Configuration.PublishedPorts {
			if p.Proto != "" && !strings.EqualFold(p.Proto, "tcp") {
				continue
			}
			if p.HostAddress != "" && p.HostAddress != "0.0.0.0" {
				continue
			}
			n := max(p.Count, 1)
			for i := range n {
				if port := p.HostPort + i; port > 0 {
					seen[port] = true
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// portForwarder answers on IPv6 for ports the runtime publishes on IPv4 and
// relays each connection to the IPv4 loopback, where the runtime listens.
type portForwarder struct {
	mu        sync.Mutex
	listeners map[int]net.Listener
	failed    map[int]bool
	target    string
	warn      func(format string, args ...any)
}

func newPortForwarder(warn func(format string, args ...any)) *portForwarder {
	return &portForwarder{
		listeners: map[int]net.Listener{},
		failed:    map[int]bool{},
		target:    "127.0.0.1",
		warn:      warn,
	}
}

// reconcile opens listeners for newly published ports and closes those no
// longer published. A port that cannot be bound, typically because another
// program holds it on IPv6, is reported once and retried on later passes.
func (f *portForwarder) reconcile(ports []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[int]bool{}
	for _, port := range ports {
		want[port] = true
		if _, ok := f.listeners[port]; ok {
			continue
		}
		// tcp6 sets IPV6_V6ONLY, so this never collides with the runtime's
		// IPv4 listener on the same port.
		ln, err := net.Listen("tcp6", net.JoinHostPort("::", strconv.Itoa(port)))
		if err != nil {
			if !f.failed[port] && f.warn != nil {
				f.warn("port %d is not reachable over IPv6: %v", port, err)
			}
			f.failed[port] = true
			continue
		}
		delete(f.failed, port)
		f.listeners[port] = ln
		go f.serve(ln, port)
	}
	for port, ln := range f.listeners {
		if !want[port] {
			_ = ln.Close()
			delete(f.listeners, port)
		}
	}
	for port := range f.failed {
		if !want[port] {
			delete(f.failed, port)
		}
	}
}

// ports returns the ports currently forwarded.
func (f *portForwarder) ports() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.listeners))
}

// close stops every listener. Connections already relaying finish on their
// own.
func (f *portForwarder) close() { f.reconcile(nil) }

func (f *portForwarder) serve(ln net.Listener, port int) {
	addr := net.JoinHostPort(f.target, strconv.Itoa(port))
	for {
		client, err := ln.Accept()
		if err != nil {
			return
		}
		go relay(client, addr)
	}
}

// relay copies bytes both ways, passing each side's end of stream on so
// half-closed protocols such as HTTP/1.0 still complete.
func relay(client net.Conn, addr string) {
	defer client.Close()
	upstream, err := net.DialTimeout("tcp4", addr, 5*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	<-done
	<-done
}
