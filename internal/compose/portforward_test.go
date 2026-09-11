package compose

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
)

func TestForwardablePorts(t *testing.T) {
	c := func(state string, ports ...engine.PublishedPort) engine.Container {
		var x engine.Container
		x.Status.State = state
		x.Configuration.PublishedPorts = ports
		return x
	}
	cs := []engine.Container{
		c("running",
			engine.PublishedPort{HostAddress: "0.0.0.0", HostPort: 8080, Proto: "tcp", Count: 1},
			engine.PublishedPort{HostAddress: "", HostPort: 9000, Proto: "tcp", Count: 3},
			engine.PublishedPort{HostAddress: "127.0.0.1", HostPort: 5432, Proto: "tcp", Count: 1},
			engine.PublishedPort{HostAddress: "0.0.0.0", HostPort: 53, Proto: "udp", Count: 1},
		),
		c("stopped", engine.PublishedPort{HostAddress: "0.0.0.0", HostPort: 7000, Proto: "tcp", Count: 1}),
		c("running", engine.PublishedPort{HostAddress: "0.0.0.0", HostPort: 8080, Proto: "TCP"}),
	}
	got := forwardablePorts(cs)
	if want := []int{8080, 9000, 9001, 9002}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v: loopback-only, UDP and stopped containers are left out", got, want)
	}
}

// ipv4EchoServer stands in for the runtime's IPv4 port forwarder.
func ipv4EchoServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				fmt.Fprintf(conn, "echo %s", line)
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestPortForwarderRelaysIPv6ToIPv4(t *testing.T) {
	if ln, err := net.Listen("tcp6", "[::1]:0"); err != nil {
		t.Skip("IPv6 loopback unavailable")
	} else {
		ln.Close()
	}
	port := ipv4EchoServer(t)
	var warnings []string
	f := newPortForwarder(func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) })
	defer f.close()
	f.reconcile([]int{port})
	if got := f.ports(); !reflect.DeepEqual(got, []int{port}) {
		t.Fatalf("forwarded %v, warnings %v", got, warnings)
	}

	conn, err := net.DialTimeout("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatalf("IPv6 client refused: %v", err)
	}
	fmt.Fprint(conn, "hello\n")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, _ := io.ReadAll(conn)
	conn.Close()
	if strings.TrimSpace(string(reply)) != "echo hello" {
		t.Fatalf("reply %q", reply)
	}

	// Unpublishing closes the IPv6 listener so the port is free again.
	f.reconcile(nil)
	if len(f.ports()) != 0 {
		t.Fatalf("listeners left open: %v", f.ports())
	}
	if c, err := net.DialTimeout("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)), 500*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("an unpublished port must stop answering on IPv6")
	}
}

func TestPortForwarderWarnsOnceWhenAPortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp6", "[::]:0")
	if err != nil {
		t.Skip("IPv6 unavailable")
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port
	var warnings []string
	f := newPortForwarder(func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) })
	defer f.close()
	f.reconcile([]int{port})
	f.reconcile([]int{port})
	if len(warnings) != 1 || !strings.Contains(warnings[0], strconv.Itoa(port)) {
		t.Fatalf("expected one warning for port %d, got %v", port, warnings)
	}
}
