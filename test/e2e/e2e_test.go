// Package e2e drives the built apple-compose binary against a live container
// runtime. It runs only when APPLE_COMPOSE_E2E=1, because it boots VMs.
package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func binary(t *testing.T) string {
	t.Helper()
	if os.Getenv("APPLE_COMPOSE_E2E") == "" {
		t.Skip("set APPLE_COMPOSE_E2E=1 to run end-to-end tests against the container runtime")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin", "apple-compose")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("build the binary first with `make build`: %v", err)
	}
	return bin
}

type cli struct {
	t   *testing.T
	bin string
	dir string
	env []string
}

func (c *cli) run(args ...string) (string, int) {
	c.t.Helper()
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.dir
	cmd.Env = append(os.Environ(), c.env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if xe, ok := err.(*exec.ExitError); ok {
		code = xe.ExitCode()
	} else if err != nil {
		c.t.Fatalf("%v: %v", args, err)
	}
	c.t.Logf("$ apple-compose %s (exit %d)\n%s", strings.Join(args, " "), code, out.String())
	return out.String(), code
}

func (c *cli) must(args ...string) string {
	c.t.Helper()
	out, code := c.run(args...)
	if code != 0 {
		c.t.Fatalf("apple-compose %v failed with %d:\n%s", args, code, out)
	}
	return out
}

func writeProject(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUpResolvesPeersAndRecreatesOnChange(t *testing.T) {
	bin := binary(t)
	dir := writeProject(t, `
name: e2ebasic
services:
  web:
    image: alpine:3.20
    command: sh -c "while true; do sleep 1; done"
    environment:
      GREETING: ${GREETING:-hello}
    depends_on:
      db:
        condition: service_healthy
      init:
        condition: service_completed_successfully
  db:
    image: alpine:3.20
    command: sh -c "while true; do sleep 1; done"
    healthcheck:
      test: ["CMD", "true"]
      interval: 1s
    networks:
      default:
        aliases: [database]
  init:
    image: alpine:3.20
    command: sh -c "echo init done"
`)
	c := &cli{t: t, bin: bin, dir: dir}
	t.Cleanup(func() { c.run("down", "-v", "--remove-orphans") })

	out := c.must("up", "-d")
	for _, want := range []string{"Container e2ebasic-db-1  Healthy", "Container e2ebasic-init-1  Exited (0)", "Container e2ebasic-web-1  Started"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in up output", want)
		}
	}

	hosts := c.must("exec", "-T", "web", "getent", "hosts", "db", "database", "e2ebasic-db-1", "host.docker.internal")
	if strings.Count(hosts, "\n") < 4 {
		t.Fatalf("expected four resolutions, got:\n%s", hosts)
	}
	back := c.must("exec", "-T", "db", "getent", "hosts", "web")
	if !strings.Contains(back, "web") {
		t.Fatalf("db cannot resolve web:\n%s", back)
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(c.must("ps", "-a", "--format", "json")), &rows); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, r := range rows {
		states[r["Service"].(string)] = r["State"].(string)
	}
	if states["web"] != "running" || states["db"] != "running" || states["init"] != "exited" {
		t.Fatalf("unexpected states %v", states)
	}

	again := c.must("up", "-d")
	if !strings.Contains(again, "Container e2ebasic-web-1  Running") {
		t.Fatalf("second up must be a no-op for web:\n%s", again)
	}

	c.env = []string{"GREETING=changed"}
	changed := c.must("up", "-d")
	if !strings.Contains(changed, "Container e2ebasic-web-1  Recreated") || strings.Contains(changed, "e2ebasic-db-1  Recreated") {
		t.Fatalf("only web should be recreated:\n%s", changed)
	}
	env := c.must("exec", "-T", "web", "sh", "-c", "echo $GREETING")
	if !strings.Contains(env, "changed") {
		t.Fatalf("environment not updated: %s", env)
	}

	if _, code := c.run("exec", "-T", "web", "sh", "-c", "exit 3"); code != 3 {
		t.Fatalf("exec exit code = %d, want 3", code)
	}
	if _, code := c.run("run", "--rm", "-T", "db", "sh", "-c", "getent hosts web && exit 5"); code != 5 {
		t.Fatalf("run exit code = %d, want 5", code)
	}

	down := c.must("down")
	if !strings.Contains(down, "Network e2ebasic_default  Removed") {
		t.Fatalf("network not removed:\n%s", down)
	}
}

func TestAttachedUpPropagatesExitCode(t *testing.T) {
	bin := binary(t)
	dir := writeProject(t, `
name: e2eattached
services:
  task:
    image: alpine:3.20
    command: sh -c "echo working; sleep 1; exit 7"
  sidecar:
    image: alpine:3.20
    command: sh -c "trap 'exit 0' TERM; while true; do sleep 1; done"
`)
	c := &cli{t: t, bin: bin, dir: dir}
	t.Cleanup(func() { c.run("down") })
	out, code := c.run("up", "--exit-code-from", "task")
	if code != 7 {
		t.Fatalf("exit code = %d, want 7:\n%s", code, out)
	}
	if !strings.Contains(out, "task    | working") {
		t.Fatalf("missing prefixed log line:\n%s", out)
	}
	ps := c.must("ps", "-a")
	if !strings.Contains(ps, "Exited (7)") {
		t.Fatalf("exit code not recorded:\n%s", ps)
	}
}

func TestDryRunNeedsNoRuntimeState(t *testing.T) {
	bin := binary(t)
	dir := writeProject(t, `
name: e2edry
services:
  web:
    image: alpine:3.20
    ports: ["18555:80"]
    volumes: ["data:/data"]
volumes:
  data: {}
`)
	c := &cli{t: t, bin: bin, dir: dir}
	out := c.must("--dry-run", "up", "-d")
	for _, want := range []string{"network create", "volume create", "--publish 18555:80/tcp", "type=volume,source=e2edry_data,target=/data", "container start e2edry-web-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in dry-run output:\n%s", want, out)
		}
	}
}
