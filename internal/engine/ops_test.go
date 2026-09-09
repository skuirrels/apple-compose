package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/enginetest"
)

func TestListAndInspect(t *testing.T) {
	f := enginetest.New(t)
	f.On("ls --format json --all", "["+enginetest.ContainerJSON("p-web-1", "web", "p", "running", "192.168.66.2", "p_default", nil)+","+enginetest.ContainerJSON("other-x-1", "x", "other", "stopped", "", "other_default", nil)+"]", 0)
	f.On("inspect missing", "Error: container not found: missing", 1)
	f.On("inspect p-web-1", "["+enginetest.ContainerJSON("p-web-1", "web", "p", "running", "192.168.66.2", "p_default", nil)+"]", 0)
	ctx := context.Background()

	cs, err := f.Engine.ProjectContainers(ctx, "p", true)
	if err != nil || len(cs) != 1 || cs[0].ID != "p-web-1" || cs[0].PrimaryIP() != "192.168.66.2" {
		t.Fatalf("project containers: %v %v", cs, err)
	}
	if _, err := f.Engine.InspectContainer(ctx, "missing"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	c, err := f.Engine.InspectContainer(ctx, "p-web-1")
	if err != nil || !c.Running() || c.Gateway() != "192.168.66.1" {
		t.Fatalf("inspect: %+v %v", c, err)
	}
}

func TestStopKillDeleteArgs(t *testing.T) {
	f := enginetest.New(t)
	ctx := context.Background()
	if err := f.Engine.Stop(ctx, []string{"a", "b"}, "SIGINT", 7*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := f.Engine.Kill(ctx, []string{"a"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.Engine.Delete(ctx, []string{"a"}, true); err != nil {
		t.Fatal(err)
	}
	if err := f.Engine.Stop(ctx, nil, "", 0); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	want := []string{"stop --signal SIGINT --time 7 a b", "kill a", "delete --force a"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestExecAndCreateArgs(t *testing.T) {
	f := enginetest.New(t)
	f.On("create", "p-web-1\n", 0)
	ctx := context.Background()
	id, err := f.Engine.Create(ctx, "--name", "p-web-1", "alpine")
	if err != nil || id != "p-web-1" {
		t.Fatalf("create: %q %v", id, err)
	}
	code, err := f.Engine.Exec(ctx, "p-web-1", engine.ExecOptions{Interactive: true, TTY: true, User: "u", WorkDir: "/w", Env: []string{"A=1"}}, []string{"sh", "-c", "x"}, nil, nil, nil)
	if err != nil || code != 0 {
		t.Fatal(err, code)
	}
	if got := f.Call("exec"); got != "exec --interactive --tty --user u --workdir /w --env A=1 p-web-1 sh -c x" {
		t.Fatalf("exec args = %q", got)
	}
}

func TestExitErrorAndDryRun(t *testing.T) {
	f := enginetest.New(t)
	f.On("network create", "Error: network exists\n", 1)
	ctx := context.Background()
	err := f.Engine.CreateNetwork(ctx, "n", engine.NetworkOptions{Subnet: "10.0.0.0/24", Labels: map[string]string{"b": "2", "a": "1"}})
	var xe *engine.ExitError
	if !errors.As(err, &xe) || xe.Code != 1 || err.Error() != "network exists" {
		t.Fatalf("unexpected error %v", err)
	}
	if got := f.Call("network create"); got != "network create --subnet 10.0.0.0/24 --label a=1 --label b=2 n" {
		t.Fatalf("args = %q", got)
	}
	f.Reset()
	f.Engine.DryRun = true
	f.Engine.Log = &strings.Builder{}
	if err := f.Engine.DeleteNetwork(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls()) != 0 {
		t.Fatal("dry run must not invoke the runtime")
	}
}

func TestImagesAndVolumes(t *testing.T) {
	f := enginetest.New(t)
	f.On("image inspect docker.io/library/alpine:3.20", `[{"id":"sha256:abc","configuration":{"name":"docker.io/library/alpine:3.20"},"variants":[{"platform":{"os":"linux","architecture":"arm64"},"size":4093973}]}]`, 0)
	f.On("image inspect nope", "Error: not found", 1)
	f.On("volume inspect v", `[{"id":"v","configuration":{"name":"v","labels":{"com.docker.compose.project":"p"}}}]`, 0)
	ctx := context.Background()
	if ok, err := f.Engine.HasImage(ctx, "docker.io/library/alpine:3.20"); err != nil || !ok {
		t.Fatal("expected image present", err)
	}
	if ok, err := f.Engine.HasImage(ctx, "nope"); err != nil || ok {
		t.Fatal("expected image absent", err)
	}
	v, err := f.Engine.InspectVolume(ctx, "v")
	if err != nil || v.Label("com.docker.compose.project") != "p" {
		t.Fatal(v, err)
	}
	if err := f.Engine.CreateVolume(ctx, "v2", map[string]string{"k": "v"}, map[string]string{"size": "1g"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("volume create"); got != "volume create --label k=v --opt size=1g v2" {
		t.Fatalf("args = %q", got)
	}
}

func TestVersionParsing(t *testing.T) {
	f := enginetest.New(t)
	f.On("--version", "container CLI version 1.3.1 (build: release, commit: a9a62e2)\n", 0)
	v, err := f.Engine.Version(context.Background())
	if err != nil || v != "1.3.1" {
		t.Fatalf("version = %q %v", v, err)
	}
}
