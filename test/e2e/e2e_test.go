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
	"time"
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
    volumes:
      - data:/data
volumes:
  data: {}
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
	empty := c.must("run", "--rm", "-T", "init", "sh", "-c", "ls -A /data | wc -l")
	if strings.TrimSpace(strings.Split(strings.TrimSpace(empty), "\n")[len(strings.Split(strings.TrimSpace(empty), "\n"))-1]) != "0" {
		t.Fatalf("fresh volume must be empty:\n%s", empty)
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

func TestPeersResolveAtBoot(t *testing.T) {
	bin := binary(t)
	dir := writeProject(t, `
name: e2eboot
services:
  db:
    image: alpine:3.20
    command: sh -c "while true; do sleep 1; done"
  client:
    image: alpine:3.20
    command: sh -c "getent hosts db && getent hosts $(hostname)"
    depends_on: [db]
`)
	c := &cli{t: t, bin: bin, dir: dir}
	t.Cleanup(func() { c.run("down") })
	out, code := c.run("up", "--exit-code-from", "client")
	if code != 0 {
		t.Fatalf("client could not resolve db or itself at boot (exit %d):\n%s", code, out)
	}
}

func TestDetachedRestartPolicy(t *testing.T) {
	bin := binary(t)
	dir := writeProject(t, `
name: e2erestart
services:
  flaky:
    image: alpine:3.20
    restart: on-failure:3
    command: sh -c "echo attempt; sleep 1; exit 1"
`)
	c := &cli{t: t, bin: bin, dir: dir}
	t.Cleanup(func() { c.run("down") })
	out := c.must("up", "-d")
	if !strings.Contains(out, "Supervisor e2erestart") {
		t.Fatalf("up -d must launch the supervisor:\n%s", out)
	}
	// The container exits after a second; the supervisor restarts it up to
	// three times before giving up.
	deadline := time.Now().Add(2 * time.Minute)
	var log string
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(filepath.Join(stateHome(t), "projects", "e2erestart", "supervisor.log"))
		log = string(b)
		if strings.Contains(log, "not restarting (on-failure:3)") {
			break
		}
		time.Sleep(2 * time.Second)
	}
	for _, want := range []string{"attempt 1", "attempt 2", "attempt 3", "not restarting (on-failure:3)", "nothing left to supervise"} {
		if !strings.Contains(log, want) {
			t.Fatalf("supervisor log lacks %q:\n%s", want, log)
		}
	}
	ps := c.must("ps", "-a")
	if !strings.Contains(ps, "Exited (1)") {
		t.Fatalf("the exit code learned by the supervisor must be visible:\n%s", ps)
	}
	// A deliberate stop must not be undone by a fresh supervisor.
	c.must("start")
	c.must("stop")
	time.Sleep(6 * time.Second)
	if ps := c.must("ps", "-a"); !strings.Contains(ps, "exited") && !strings.Contains(ps, "Exited") {
		t.Fatalf("stopped container must stay stopped:\n%s", ps)
	}
}

func stateHome(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("APPLE_COMPOSE_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, "Library", "Application Support", "apple-compose")
}

func TestDockerCLIFrontEnd(t *testing.T) {
	bin := binary(t)
	docker := filepath.Join(filepath.Dir(bin), "apple-docker")
	if _, err := os.Stat(docker); err != nil {
		t.Fatalf("build apple-docker first with `make build`: %v", err)
	}
	c := &cli{t: t, bin: docker, dir: t.TempDir()}
	t.Cleanup(func() { c.run("rm", "-f", "e2edocker") })
	out := c.must("run", "-d", "--name", "e2edocker", "-e", "GREETING=hello", "-l", "suite=e2e", "alpine:3.20", "sh", "-c", "echo $GREETING; sleep 30")
	if !strings.Contains(out, "e2edocker") {
		t.Fatalf("run -d must print the container id:\n%s", out)
	}
	ps := c.must("ps", "--filter", "label=suite=e2e", "--format", "{{.Names}} {{.State}}")
	if !strings.Contains(ps, "e2edocker running") {
		t.Fatalf("ps: %s", ps)
	}
	ip := c.must("inspect", "-f", "{{.NetworkSettings.IPAddress}} {{.State.Running}}", "e2edocker")
	if !strings.Contains(ip, "true") || !strings.Contains(ip, ".") {
		t.Fatalf("inspect: %s", ip)
	}
	if got := c.must("exec", "e2edocker", "sh", "-c", "echo $GREETING"); !strings.Contains(got, "hello") {
		t.Fatalf("exec: %s", got)
	}
	if got := c.must("logs", "e2edocker"); !strings.Contains(got, "hello") {
		t.Fatalf("logs: %s", got)
	}
	if _, code := c.run("run", "--rm", "alpine:3.20", "sh", "-c", "exit 6"); code != 6 {
		t.Fatalf("attached run must return the process exit code, got %d", code)
	}
	if got := c.must("stop", "-t", "1", "e2edocker"); strings.TrimSpace(got) != "e2edocker" {
		t.Fatalf("stop must echo the name: %q", got)
	}
	if got := c.must("ps", "-a", "--filter", "name=e2edocker", "--format", "{{.Status}}"); !strings.Contains(got, "Exited") {
		t.Fatalf("stopped status: %s", got)
	}
	if got := c.must("rm", "e2edocker"); strings.TrimSpace(got) != "e2edocker" {
		t.Fatalf("rm must echo the name: %q", got)
	}
	if got := c.must("images", "--filter", "reference=alpine*", "--format", "{{.Repository}}:{{.Tag}}"); !strings.Contains(got, "alpine:3.20") {
		t.Fatalf("images: %s", got)
	}
	if got := c.must("network", "ls", "--format", "{{.Name}}"); !strings.Contains(got, "default") {
		t.Fatalf("network ls: %s", got)
	}
}

func TestDockerRunResolvesNamesOnUserNetwork(t *testing.T) {
	bin := binary(t)
	docker := filepath.Join(filepath.Dir(bin), "apple-docker")
	if _, err := os.Stat(docker); err != nil {
		t.Fatalf("build apple-docker first with `make build`: %v", err)
	}
	c := &cli{t: t, bin: docker, dir: t.TempDir()}
	const net = "e2enames"
	t.Cleanup(func() {
		c.run("rm", "-f", "e2eserver", "e2eclient")
		c.run("network", "rm", net)
	})
	c.must("network", "create", net)
	c.must("run", "-d", "--name", "e2eserver", "--network", net, "--network-alias", "api",
		"alpine:3.20", "sh", "-c", "while true; do sleep 1; done")

	// A container joining the network resolves the peers already on it, by
	// name and by alias, and honours --add-host.
	out, code := c.run("run", "--rm", "--network", net, "--add-host", "fixed:10.9.9.9",
		"alpine:3.20", "sh", "-c", "getent hosts e2eserver && getent hosts api && getent hosts fixed")
	if code != 0 {
		t.Fatalf("names on a user-defined network must resolve (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "10.9.9.9") {
		t.Fatalf("--add-host must reach the container:\n%s", out)
	}

	// Containers already running learn about newcomers, as Docker's DNS
	// would tell them.
	c.must("run", "-d", "--name", "e2eclient", "--network", net, "alpine:3.20", "sh", "-c", "while true; do sleep 1; done")
	if got := c.must("exec", "e2eserver", "getent", "hosts", "e2eclient"); !strings.Contains(got, "e2eclient") {
		t.Fatalf("a running container must learn a newcomer's address:\n%s", got)
	}
}

func TestWatchSyncsChangedFiles(t *testing.T) {
	bin := binary(t)
	dir := writeProject(t, `
name: e2ewatch
services:
  app:
    image: alpine:3.20
    command: sh -c "while true; do sleep 1; done"
    develop:
      watch:
        - path: ./src
          action: sync
          target: /srv/app
          ignore: [ignored]
`)
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &cli{t: t, bin: bin, dir: dir}
	t.Cleanup(func() { c.run("down") })
	c.must("up", "-d")

	logPath := filepath.Join(t.TempDir(), "watch.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	watch := exec.Command(bin, "watch", "--no-up", "--prune", "--interval", "200ms")
	watch.Dir = dir
	watch.Stdout, watch.Stderr = logFile, logFile
	if err := watch.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = watch.Process.Signal(os.Interrupt)
		_ = watch.Wait()
		b, _ := os.ReadFile(logPath)
		t.Logf("watch output:\n%s", b)
	})
	// The baseline scan happens before the watch says it is watching, so
	// wait for that line: a file written earlier would be part of the
	// baseline and never reported as a change.
	waitForLog(t, logPath, "watching")

	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A file created after the watch starts must reach the container, and an
	// ignored directory must not.
	write("hello.txt", "one")
	if err := os.WriteFile(filepath.Join(src, "ignored", "skip.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForContent(t, c, "/srv/app/hello.txt", "one")
	// A rewrite reaches it too.
	write("hello.txt", "two")
	waitForContent(t, c, "/srv/app/hello.txt", "two")
	if out, _ := c.run("exec", "app", "sh", "-c", "ls /srv/app"); strings.Contains(out, "skip.txt") {
		t.Fatalf("an ignored path must not be synced:\n%s", out)
	}
	// --prune removes what the host no longer has.
	if err := os.Remove(filepath.Join(src, "hello.txt")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, code := c.run("exec", "app", "test", "-f", "/srv/app/hello.txt"); code != 0 {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("--prune must delete a file the host no longer has")
}

// waitForLog blocks until a log file contains want.
func waitForLog(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), want) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("watch never reported %q", want)
}

// waitForContent blocks until a file inside the app container holds want.
func waitForContent(t *testing.T, c *cli, path, want string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, code := c.run("exec", "app", "cat", path)
		if code == 0 && strings.TrimSpace(out) == want {
			return
		}
		last = out
		time.Sleep(time.Second)
	}
	t.Fatalf("%s never held %q, last read:\n%s", path, want, last)
}
