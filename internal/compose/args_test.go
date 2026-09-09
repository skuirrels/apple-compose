package compose

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// loadRunner loads a compose document from a temporary directory and wraps it
// in a Runner whose console writes to buffers and whose state lives in a
// temporary APPLE_COMPOSE_HOME.
func loadRunner(t *testing.T, yaml string, files map[string]string) (*Runner, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPLE_COMPOSE_HOME", filepath.Join(dir, "state"))
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(context.Background(), project.Options{ConfigPaths: []string{filepath.Join(dir, "compose.yaml")}, WorkingDir: dir}, "test")
	if err != nil {
		t.Fatal(err)
	}
	errBuf := &bytes.Buffer{}
	console := &ui.Console{Out: &bytes.Buffer{}, Err: errBuf}
	return New(&engine.Engine{Bin: "/nonexistent"}, p, console, "test"), errBuf
}

func argsFor(t *testing.T, r *Runner, service string) string {
	t.Helper()
	s, err := r.Project.GetService(service)
	if err != nil {
		t.Fatal(err)
	}
	args, err := r.createArgs(createSpec{service: s, name: project.ContainerName(r.Project, s, 1), number: 1, hash: "h", hostsPath: "/tmp/hosts"})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(args, " ")
}

func TestCreateArgsCoreKeys(t *testing.T) {
	r, warnings := loadRunner(t, `
name: t
services:
  web:
    image: nginx:alpine
    command: ["nginx", "-g", "daemon off;"]
    environment:
      A: "1"
      EMPTY: ""
      UNSET:
    working_dir: /srv
    user: "1000:1000"
    ports:
      - "8080:80"
      - "127.0.0.1:9000-9001:9000/udp"
    volumes:
      - ./site:/site:ro
      - data:/data
      - /anon
      - type: tmpfs
        target: /cache
        tmpfs:
          size: 64m
    tmpfs:
      - /run
    networks:
      front:
        mac_address: "02:42:ac:11:00:02"
      back: {}
    dns: [1.1.1.1]
    dns_search: [example.internal]
    cap_add: [NET_ADMIN]
    cap_drop: [ALL]
    read_only: true
    init: true
    shm_size: 128m
    ulimits:
      nofile:
        soft: 1024
        hard: 2048
      nproc: 100
    cpus: 1.5
    mem_limit: 512m
    platform: linux/arm64
    stop_signal: SIGQUIT
    stop_grace_period: 15s
    tty: true
    labels:
      com.example.a: b
    x-apple-compose:
      args: ["--ssh"]
networks:
  front: {}
  back: {}
volumes:
  data: {}
`, map[string]string{"site/index.html": "hi"})
	args := argsFor(t, r, "web")
	for _, want := range []string{
		"--name t-web-1",
		"--label com.apple-compose.stop-grace=15s",
		"--label com.apple-compose.stop-signal=SIGQUIT",
		"--label com.docker.compose.config-hash=h",
		"--label com.docker.compose.project=t",
		"--label com.docker.compose.service=web",
		"--label com.example.a=b",
		"--platform linux/arm64",
		"--env A=1", "--env EMPTY=",
		"--workdir /srv", "--user 1000:1000",
		"--cpus 2", "--memory 512M",
		"--publish 8080:80/tcp",
		"--publish 127.0.0.1:9000:9000/udp --publish 127.0.0.1:9001:9001/udp",
		",target=/site,readonly",
		"--mount type=volume,source=t_data,target=/data",
		"--volume /anon",
		"--tmpfs /cache:size=64M",
		"--tmpfs /run",
		"--volume /tmp/hosts:/etc/hosts",
		"--network t_back --network t_front,mac=02:42:ac:11:00:02",
		"--dns 1.1.1.1", "--dns-search example.internal",
		"--cap-add NET_ADMIN", "--cap-drop ALL",
		"--read-only", "--init", "--shm-size 128M",
		"--ulimit nofile=1024:2048", "--ulimit nproc=100",
		"--tty", "--ssh",
		"nginx:alpine nginx -g daemon off;",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in:\n%s", want, args)
		}
	}
	if strings.Contains(args, "UNSET") {
		t.Errorf("unset variable must be omitted:\n%s", args)
	}
	if !strings.Contains(args, "type=bind,source=") {
		t.Errorf("bind mount missing:\n%s", args)
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %s", warnings.String())
	}
}

func TestCreateArgsEntrypointVariants(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  a:
    image: img
    entrypoint: ["/bin/sh", "-c"]
    command: "echo hi"
  b:
    image: img
    entrypoint: []
    command: ["python", "app.py"]
  c:
    image: img
`, nil)
	if got := argsFor(t, r, "a"); !strings.HasSuffix(got, "img --entrypoint /bin/sh -c echo hi") {
		t.Errorf("a: %s", got)
	}
	if got := argsFor(t, r, "b"); !strings.HasSuffix(got, "img --entrypoint python app.py") {
		t.Errorf("b: %s", got)
	}
	if got := argsFor(t, r, "c"); !strings.HasSuffix(got, " img") {
		t.Errorf("c: %s", got)
	}
}

func TestCreateArgsSecretsConfigsAndBuildImage(t *testing.T) {
	t.Setenv("TOKEN", "s3cret")
	r, _ := loadRunner(t, `
name: t
services:
  app:
    build: ./app
    secrets:
      - source: tok
        target: api_token
      - cert
    configs:
      - source: cfg
        target: /etc/app.conf
secrets:
  tok:
    environment: TOKEN
  cert:
    file: ./cert.pem
configs:
  cfg:
    content: "key=value"
`, map[string]string{"app/Dockerfile": "FROM alpine", "cert.pem": "pem"})
	args := argsFor(t, r, "app")
	if !strings.Contains(args, ":/run/secrets/api_token:ro") || !strings.Contains(args, "cert.pem:/run/secrets/cert:ro") || !strings.Contains(args, ":/etc/app.conf:ro") {
		t.Errorf("secret/config mounts missing:\n%s", args)
	}
	if !strings.Contains(args, " t-app") {
		t.Errorf("built image name missing:\n%s", args)
	}
	dir, _ := os.ReadDir(filepath.Join(os.Getenv("APPLE_COMPOSE_HOME"), "projects", "t", "secrets"))
	if len(dir) != 2 {
		t.Errorf("expected two materialised secret files, got %d", len(dir))
	}
	b, _ := os.ReadFile(filepath.Join(os.Getenv("APPLE_COMPOSE_HOME"), "projects", "t", "secrets", "secret-tok"))
	if string(b) != "s3cret" {
		t.Errorf("secret content = %q", b)
	}
}

func TestCreateArgsWarningsAndErrors(t *testing.T) {
	r, warnings := loadRunner(t, `
name: t
services:
  a:
    image: img
    privileged: true
    restart: always
    devices: ["/dev/null:/dev/null"]
    hostname: custom
    sysctls:
      net.core.somaxconn: "1024"
  h:
    image: img
    network_mode: host
  n:
    image: img
    network_mode: none
  e:
    image: img
    ports:
      - target: 80
`, nil)
	args := argsFor(t, r, "a")
	if !strings.Contains(args, "--cap-add ALL") {
		t.Errorf("privileged should map to all capabilities:\n%s", args)
	}
	for _, want := range []string{"privileged", "`devices`", "hostname", "`sysctls`"} {
		if !strings.Contains(warnings.String(), want) {
			t.Errorf("missing warning about %s in:\n%s", want, warnings.String())
		}
	}
	if got := argsFor(t, r, "n"); !strings.Contains(got, "--network none") {
		t.Errorf("network none: %s", got)
	}
	for _, svc := range []string{"h", "e"} {
		s, _ := r.Project.GetService(svc)
		if _, err := r.createArgs(createSpec{service: s, name: "x", number: 1}); err == nil {
			t.Errorf("service %s should fail translation", svc)
		}
	}
}

func TestBindBackedVolumes(t *testing.T) {
	r, warnings := loadRunner(t, `
name: t
services:
  a:
    image: img
    volumes: ["shared:/s", "docker:/d", "plain:/p"]
  b:
    image: img
    volumes: ["plain:/p"]
volumes:
  shared:
    x-apple-compose:
      shared: true
  docker:
    driver_opts:
      type: none
      o: bind
      device: ./dockerdata
  plain: {}
`, nil)
	args := argsFor(t, r, "a")
	if !strings.Contains(args, "type=bind,source="+filepath.Join(os.Getenv("APPLE_COMPOSE_HOME"), "volumes", "t_shared")+",target=/s") {
		t.Errorf("shared volume should be host backed:\n%s", args)
	}
	if !strings.Contains(args, "dockerdata,target=/d") || !strings.Contains(args, "type=volume,source=t_plain,target=/p") {
		t.Errorf("driver_opts bind or plain volume wrong:\n%s", args)
	}
	r.warnSharedVolumes()
	if !strings.Contains(warnings.String(), "volume plain is mounted by a, b") {
		t.Errorf("expected shared volume warning, got: %s", warnings.String())
	}
}
