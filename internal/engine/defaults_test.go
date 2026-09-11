package engine_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/enginetest"
)

// probedEngine returns an engine whose host looks like the given machine and
// whose networks all have the gateway 192.168.65.1.
func probedEngine(t *testing.T, memGiB uint64, cpus int, gatewayAnswers bool, resolvers []string) (*engine.Engine, *int) {
	t.Helper()
	for _, k := range []string{engine.EnvDefaultMemory, engine.EnvDefaultCPUs, engine.EnvDefaultDNS} {
		t.Setenv(k, "")
	}
	f := enginetest.New(t)
	f.On("network inspect", `[{"id":"default","configuration":{"name":"default"},"status":{"ipv4Gateway":"192.168.65.1"}}]`, 0)
	probes := 0
	e := f.Engine
	e.NoHostDefaults = false
	e.Probe = &engine.HostProbe{
		MemoryBytes: func() uint64 { return memGiB << 30 },
		CPUs:        func() int { return cpus },
		DNSAnswers: func(addr string) bool {
			probes++
			if addr != "192.168.65.1:53" {
				t.Errorf("probed %s, want the network gateway", addr)
			}
			return gatewayAnswers
		},
		Resolvers: func() []string { return resolvers },
	}
	return e, &probes
}

func TestDefaultMemoryIsHalfTheHostWithAFloor(t *testing.T) {
	e, _ := probedEngine(t, 24, 14, true, nil)
	if got := e.DefaultMemory(); got != "12g" {
		t.Fatalf("24 GiB host: got %q, want 12g", got)
	}
	e, _ = probedEngine(t, 3, 4, true, nil)
	if got := e.DefaultMemory(); got != "2g" {
		t.Fatalf("small host must still get the 2g floor, got %q", got)
	}
	t.Setenv(engine.EnvDefaultMemory, "6g")
	if got := e.DefaultMemory(); got != "6g" {
		t.Fatalf("environment must win, got %q", got)
	}
	t.Setenv(engine.EnvDefaultMemory, engine.RuntimeDefault)
	if got := e.DefaultMemory(); got != "" {
		t.Fatalf("runtime keyword must pass no flag, got %q", got)
	}
}

func TestDefaultCPUsIsEveryHostCPU(t *testing.T) {
	e, _ := probedEngine(t, 24, 14, true, nil)
	if got := e.DefaultCPUs(); got != 14 {
		t.Fatalf("got %d, want 14", got)
	}
	t.Setenv(engine.EnvDefaultCPUs, "2")
	if got := e.DefaultCPUs(); got != 2 {
		t.Fatalf("environment must win, got %d", got)
	}
	t.Setenv(engine.EnvDefaultCPUs, engine.RuntimeDefault)
	if got := e.DefaultCPUs(); got != 0 {
		t.Fatalf("runtime keyword must pass no flag, got %d", got)
	}
}

func TestDefaultDNSKeepsAWorkingGatewayResolver(t *testing.T) {
	e, probes := probedEngine(t, 24, 14, true, []string{"10.0.0.53"})
	if got := e.DefaultDNS(context.Background(), "default"); got != nil {
		t.Fatalf("a gateway that answers must be left alone, got %v", got)
	}
	e.DefaultDNS(context.Background(), "default")
	if *probes != 1 {
		t.Fatalf("the gateway must be probed once per process, probed %d times", *probes)
	}
}

func TestDefaultDNSReplacesASilentGateway(t *testing.T) {
	ctx := context.Background()
	e, _ := probedEngine(t, 24, 14, false, []string{"127.0.2.2", "fe80::1", "10.0.0.53"})
	if got := e.DefaultDNS(ctx, "app"); !reflect.DeepEqual(got, []string{"10.0.0.53"}) {
		t.Fatalf("reachable host resolvers must be used, got %v", got)
	}
	e, _ = probedEngine(t, 24, 14, false, []string{"127.0.2.2", "127.0.2.3"})
	if got := e.DefaultDNS(ctx, "app"); !reflect.DeepEqual(got, []string{"1.1.1.1", "8.8.8.8"}) {
		t.Fatalf("loopback-only host resolvers must fall back to public ones, got %v", got)
	}
	if got := e.DefaultDNS(ctx, "none"); got != nil {
		t.Fatalf("a container without networks needs no nameservers, got %v", got)
	}
}

func TestDefaultDNSEnvironment(t *testing.T) {
	e, probes := probedEngine(t, 24, 14, false, nil)
	t.Setenv(engine.EnvDefaultDNS, "9.9.9.9, 1.0.0.1")
	if got := e.DefaultDNS(context.Background(), "default"); !reflect.DeepEqual(got, []string{"9.9.9.9", "1.0.0.1"}) {
		t.Fatalf("environment must win, got %v", got)
	}
	t.Setenv(engine.EnvDefaultDNS, engine.RuntimeDefault)
	if got := e.DefaultDNS(context.Background(), "default"); got != nil {
		t.Fatalf("runtime keyword must keep the runtime resolver, got %v", got)
	}
	if *probes != 0 {
		t.Fatalf("an explicit setting must not probe, probed %d times", *probes)
	}
}
