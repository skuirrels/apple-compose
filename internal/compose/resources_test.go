package compose

import (
	"context"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/enginetest"
)

func withFake(t *testing.T, r *Runner) *enginetest.Fake {
	t.Helper()
	f := enginetest.New(t)
	r.Engine = f.Engine
	// A real supervisor would be this test binary; tests that check
	// spawning install their own recorder.
	r.SpawnSupervisor = func(string, []string, string) (int, error) { return 0, nil }
	return f
}

func TestEnsureNetworksAndVolumes(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  a:
    image: img
    networks: [front, ext]
    volumes: ["data:/d", "extvol:/e", "shared:/s"]
networks:
  front:
    internal: true
    ipam:
      config:
        - subnet: 10.50.0.0/24
    labels:
      tier: web
  ext:
    external: true
    name: existing_net
volumes:
  data:
    driver_opts:
      size: 2g
      bogus: x
  extvol:
    external: true
  shared:
    x-apple-compose:
      shared: true
`, nil)
	f := withFake(t, r)
	f.On("network inspect", "Error: not found", 1)
	f.On("network inspect existing_net", `[{"id":"existing_net","configuration":{"name":"existing_net"}}]`, 0)
	f.On("volume inspect", "Error: not found", 1)
	f.On("volume inspect extvol", `[{"id":"extvol","configuration":{"name":"extvol"}}]`, 0)
	f.On("image inspect", "Error: not found", 1)
	ctx := context.Background()
	if err := r.EnsureNetworks(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("network create"); got != "network create --subnet 10.50.0.0/24 --internal --label com.docker.compose.network=front --label com.docker.compose.project=t --label com.docker.compose.version=test --label tier=web t_front" {
		t.Fatalf("network create = %q", got)
	}
	if strings.Contains(strings.Join(f.Calls(), "|"), "network create --subnet 10.50.0.0/24 --internal --label com.docker.compose.network=ext") {
		t.Fatal("external network must not be created")
	}
	f.Reset()
	if err := r.EnsureVolumes(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("volume create"); got != "volume create --label com.docker.compose.project=t --label com.docker.compose.version=test --label com.docker.compose.volume=data --opt size=2g t_data" {
		t.Fatalf("volume create = %q", got)
	}
	calls := strings.Join(f.Calls(), "|")
	if strings.Contains(calls, "t_shared") || strings.Contains(calls, "volume create --label com.docker.compose.project=t --label com.docker.compose.version=test --label com.docker.compose.volume=extvol") {
		t.Fatalf("shared and external volumes must not be created: %s", calls)
	}
}

func TestEnsureNetworksMissingExternal(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  a:
    image: img
    networks: [ext]
networks:
  ext:
    external: true
`, nil)
	f := withFake(t, r)
	f.On("network inspect", "Error: not found", 1)
	err := r.EnsureNetworks(context.Background())
	if err == nil || !strings.Contains(err.Error(), "declared as external") {
		t.Fatalf("expected external network error, got %v", err)
	}
}

func TestEnsureImagePolicies(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  present:
    image: docker.io/library/alpine:3.20
  absent:
    image: ghcr.io/example/missing:1
  never:
    image: ghcr.io/example/missing:2
    pull_policy: never
  always:
    image: docker.io/library/alpine:3.20
    pull_policy: always
  built:
    build:
      context: ./app
      dockerfile: Dockerfile.dev
      args:
        A: "1"
      target: prod
      labels:
        built: "yes"
`, map[string]string{"app/Dockerfile.dev": "FROM alpine"})
	f := withFake(t, r)
	f.On("image inspect", "Error: not found", 1)
	f.On("image inspect docker.io/library/alpine:3.20", `[{"id":"x","configuration":{"name":"docker.io/library/alpine:3.20"}}]`, 0)
	ctx := context.Background()
	get := func(n string) (svc struct{ name string }) { return }
	_ = get
	for _, tc := range []struct {
		service string
		wantErr bool
		call    string
	}{
		{"present", false, ""},
		{"absent", false, "image pull ghcr.io/example/missing:1"},
		{"never", true, ""},
		{"always", false, "image pull docker.io/library/alpine:3.20"},
		{"built", false, "build --tag t-built --file "},
	} {
		f.Reset()
		s, _ := r.Project.GetService(tc.service)
		err := r.EnsureImage(ctx, s, ImageOptions{Quiet: true})
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v", tc.service, err)
		}
		if tc.call == "" && (f.Called("image pull") || f.Called("build")) {
			t.Errorf("%s: unexpected pull/build: %v", tc.service, f.Calls())
		}
		if tc.call != "" && !f.Called(tc.call) {
			t.Errorf("%s: expected call %q, got %v", tc.service, tc.call, f.Calls())
		}
	}
	build := f.Call("build")
	for _, want := range []string{"--build-arg A=1", "--target prod", "--label built=yes", "--progress plain", "Dockerfile.dev"} {
		if !strings.Contains(build, want) {
			t.Errorf("build args missing %q: %s", want, build)
		}
	}
	if strings.Contains(build, "--quiet") {
		t.Errorf("quiet must not reach the runtime: %s", build)
	}
}

func TestEmptyVolumeUsesServicePlatform(t *testing.T) {
	r, _ := loadRunner(t, "name: t\nservices:\n  sql:\n    image: mcr.microsoft.com/mssql/server:2022-latest\n    platform: linux/amd64\n    volumes: [\"data:/var/opt/mssql\"]\nvolumes:\n  data: {}\n", nil)
	f := withFake(t, r)
	f.On("image inspect", `[{"id":"x","configuration":{"name":"mcr.microsoft.com/mssql/server:2022-latest"}}]`, 0)
	f.On("volume inspect", "Error: not found", 1)
	if err := r.EnsureVolumes(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := f.Call("run --rm")
	if !strings.Contains(got, "--user 0:0") || !strings.Contains(got, "--platform linux/amd64 --entrypoint /bin/sh mcr.microsoft.com/mssql/server:2022-latest -c ") || !strings.HasSuffix(got, " sh /.apple-compose-volume /var/opt/mssql") {
		t.Fatalf("emptying must run as root on the image's platform and chown to the mount point's owner: %q", got)
	}
}
