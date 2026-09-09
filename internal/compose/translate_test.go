package compose

import (
	"reflect"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestProcessArgs(t *testing.T) {
	cases := []struct {
		name       string
		entrypoint []string
		set        bool
		command    []string
		wantFlags  []string
		want       []string
		wantErr    bool
	}{
		{"command only", nil, false, []string{"echo", "hi"}, nil, []string{"echo", "hi"}, false},
		{"nothing", nil, false, nil, nil, nil, false},
		{"entrypoint list", []string{"/bin/sh", "-c"}, true, []string{"echo hi"}, []string{"--entrypoint", "/bin/sh"}, []string{"-c", "echo hi"}, false},
		{"entrypoint only", []string{"/app/run"}, true, nil, []string{"--entrypoint", "/app/run"}, nil, false},
		{"cleared entrypoint", []string{}, true, []string{"python", "app.py"}, []string{"--entrypoint", "python"}, []string{"app.py"}, false},
		{"cleared entrypoint without command", []string{}, true, nil, nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags, got, err := processArgs(tc.entrypoint, tc.command, tc.set)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !reflect.DeepEqual(flags, tc.wantFlags) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v %v, want %v %v", flags, got, tc.wantFlags, tc.want)
			}
		})
	}
}

func TestPublishSpecs(t *testing.T) {
	specs, err := publishSpecs("web", types.ServicePortConfig{Target: 80, Published: "8080"})
	if err != nil || !reflect.DeepEqual(specs, []string{"8080:80/tcp"}) {
		t.Fatalf("simple: %v %v", specs, err)
	}
	// A host range with a single target maps one free host port, as Docker
	// does; compose-go expands equal-length ranges before we see them.
	specs, err = publishSpecs("web", types.ServicePortConfig{Target: 5000, Published: "47130-47132", HostIP: "127.0.0.1", Protocol: "udp"})
	want := []string{"127.0.0.1:47130:5000/udp"}
	if err != nil || !reflect.DeepEqual(specs, want) {
		t.Fatalf("range: %v %v", specs, err)
	}
	if _, err := publishSpecs("web", types.ServicePortConfig{Target: 80}); err == nil {
		t.Fatal("expected error for ephemeral host port")
	}
}

func TestResources(t *testing.T) {
	s := types.ServiceConfig{CPUS: 0.5, MemLimit: 300 * 1024 * 1024}
	if got := cpusFor(s); got != 1 {
		t.Fatalf("cpus = %d, want 1", got)
	}
	if got := megabytes(memoryFor(s)); got != "300M" {
		t.Fatalf("memory = %s, want 300M", got)
	}
	s = types.ServiceConfig{Deploy: &types.DeployConfig{Resources: types.Resources{Limits: &types.Resource{NanoCPUs: 2.5, MemoryBytes: 1}}}}
	if got := cpusFor(s); got != 3 {
		t.Fatalf("deploy cpus = %d, want 3", got)
	}
	if got := megabytes(memoryFor(s)); got != "1M" {
		t.Fatalf("tiny memory = %s, want 1M", got)
	}
	if cpusFor(types.ServiceConfig{}) != 0 || memoryFor(types.ServiceConfig{}) != 0 {
		t.Fatal("unset resources must be zero")
	}
}

func TestParseRange(t *testing.T) {
	lo, hi, err := parseRange("10-12")
	if err != nil || lo != 10 || hi != 12 {
		t.Fatalf("got %d %d %v", lo, hi, err)
	}
	if _, _, err := parseRange("12-10"); err == nil {
		t.Fatal("expected error for reversed range")
	}
	if _, _, err := parseRange("x"); err == nil {
		t.Fatal("expected error for junk")
	}
}

func TestExtension(t *testing.T) {
	s := types.ServiceConfig{Name: "web", Extensions: types.Extensions{ExtensionKey: map[string]any{
		"args": []any{"--ssh"}, "hosts_file": false, "rosetta": true,
	}}}
	ext, err := extensionFor(s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ext.Args, []string{"--ssh"}) || ext.HostsFile || !ext.Rosetta {
		t.Fatalf("unexpected extension %+v", ext)
	}
	s.Extensions[ExtensionKey] = map[string]any{"bogus": 1}
	if _, err := extensionFor(s); err == nil {
		t.Fatal("expected error for unknown key")
	}
	if ext, _ := extensionFor(types.ServiceConfig{}); !ext.HostsFile {
		t.Fatal("hosts_file must default to true")
	}
}

func TestShouldRestart(t *testing.T) {
	if !shouldRestart("always", 0) || !shouldRestart("unless-stopped", 3) {
		t.Fatal("always/unless-stopped must restart")
	}
	if shouldRestart("on-failure", 0) || !shouldRestart("on-failure:3", 1) {
		t.Fatal("on-failure must restart only on non-zero")
	}
	if shouldRestart("no", 1) || shouldRestart("", 1) {
		t.Fatal("no policy must not restart")
	}
}

func TestServiceHashIgnoresProjectLabels(t *testing.T) {
	a := types.ServiceConfig{Name: "web", Image: "alpine", CustomLabels: types.Labels{"x": "1"}}
	b := types.ServiceConfig{Name: "web", Image: "alpine", CustomLabels: types.Labels{"x": "2"}}
	ha, _ := ServiceHash(a)
	hb, _ := ServiceHash(b)
	if ha != hb {
		t.Fatal("custom labels must not affect the hash")
	}
	c := types.ServiceConfig{Name: "web", Image: "alpine:3"}
	hc, _ := ServiceHash(c)
	if hc == ha {
		t.Fatal("image change must change the hash")
	}
}

func TestHealthcheckFor(t *testing.T) {
	retries := uint64(2)
	s := types.ServiceConfig{HealthCheck: &types.HealthCheckConfig{Test: []string{"CMD-SHELL", "curl -f localhost"}, Retries: &retries}}
	spec := healthcheckFor(s)
	if spec == nil || spec.retries != 2 || !reflect.DeepEqual(spec.cmd, []string{"/bin/sh", "-c", "curl -f localhost"}) {
		t.Fatalf("unexpected spec %+v", spec)
	}
	if healthcheckFor(types.ServiceConfig{HealthCheck: &types.HealthCheckConfig{Test: []string{"NONE"}}}) != nil {
		t.Fatal("NONE must disable")
	}
	if healthcheckFor(types.ServiceConfig{}) != nil {
		t.Fatal("no healthcheck must be nil")
	}
}

func TestStopGroups(t *testing.T) {
	cs := []engineContainer{
		{"a", map[string]string{LabelStopSignal: "SIGINT", LabelStopGrace: "3s"}},
		{"b", map[string]string{LabelStopSignal: "SIGINT", LabelStopGrace: "3s"}},
		{"c", nil},
	}
	groups := stopGroups(toContainers(cs), 0)
	if len(groups) != 2 || len(groups[0].ids) != 2 || groups[0].signal != "SIGINT" || groups[1].timeout != DefaultStopTimeout {
		t.Fatalf("unexpected groups %+v", groups)
	}
	groups = stopGroups(toContainers(cs), 7e9)
	if len(groups) != 2 || groups[0].timeout != 7e9 {
		t.Fatalf("override not applied: %+v", groups)
	}
}
