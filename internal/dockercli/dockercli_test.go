package dockercli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/compose"
	"github.com/skuirrels/apple-compose/internal/enginetest"
	"github.com/skuirrels/apple-compose/internal/ui"
)

type harness struct {
	app  *App
	fake *enginetest.Fake
	out  *bytes.Buffer
	err  *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := enginetest.New(t)
	out, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{version: "test", eng: f.Engine, console: &ui.Console{Out: out, Err: errBuf}, in: strings.NewReader("")}
	app.execFn = func(bin string, args []string) error { return nil }
	return &harness{app: app, fake: f, out: out, err: errBuf}
}

// run executes a command line and returns the process exit code.
func (h *harness) run(args ...string) int {
	h.app.execd = nil
	root := h.app.rootCommand()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		if ec, ok := err.(ExitCodeError); ok {
			return ec.Code
		}
		h.err.WriteString("Error: " + err.Error() + "\n")
		return 1
	}
	return 0
}

// execd returns the runtime command the last invocation replaced itself with.
func (h *harness) execd() string {
	if len(h.app.execd) == 0 {
		return ""
	}
	return strings.Join(h.app.execd[1:], " ")
}

func TestRunTranslatesDockerFlags(t *testing.T) {
	h := newHarness(t)
	code := h.run("run", "-d", "--name", "web", "-p", "8080:80", "-p", "127.0.0.1:9000:9000/udp", "-e", "A=1", "--env-file", ".env",
		"-v", "/tmp:/host:ro", "--mount", "type=volume,source=data,target=/data", "--tmpfs", "/run:size=64m",
		"--network", "front", "--dns", "1.1.1.1", "--entrypoint", "sh", "--cpus", "1.5", "-m", "512m",
		"--cap-add", "NET_ADMIN", "--read-only", "--init", "--shm-size", "1g", "--ulimit", "nofile=1024:2048",
		"-w", "/srv", "-u", "1000:1000", "-l", "tier=web", "--restart", "always", "-h", "myhost", "--privileged",
		"--security-opt", "seccomp=unconfined", "alpine:3.20", "sh", "-c", "echo hi")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	want := "run --name web --detach --env A=1 --env-file .env --workdir /srv --user 1000:1000 --label tier=web --publish 8080:80 --publish 127.0.0.1:9000:9000/udp --volume /tmp:/host:ro --mount type=volume,source=data,target=/data --tmpfs /run --network front --dns 1.1.1.1 --entrypoint sh --cpus 2 --memory 512m --cap-add ALL --cap-add NET_ADMIN --read-only --init --shm-size 1g --ulimit nofile=1024:2048 --label com.apple-compose.restart=always --label com.docker.compose.project=apple-docker --label com.docker.compose.service=web --label com.docker.compose.oneoff=False alpine:3.20 sh -c echo hi"
	// A detached run with a restart policy stays in-process so the
	// supervisor can be started afterwards.
	if got := h.fake.Call("run"); got != want {
		t.Fatalf("translated run =\n%s\nwant\n%s", got, want)
	}
	for _, w := range []string{"rounded up to 2", "--hostname is ignored", "--privileged has no equivalent", "--security-opt has no equivalent"} {
		if !strings.Contains(h.err.String(), w) {
			t.Fatalf("missing warning %q:\n%s", w, h.err)
		}
	}
}

func TestRunWithRestartStartsSupervisor(t *testing.T) {
	h := newHarness(t)
	t.Setenv("APPLE_COMPOSE_HOME", t.TempDir())
	var spawned []string
	h.app.spawnFn = func(name string, _ []string, _ string, log string) (int, error) {
		spawned = append(spawned, name+" "+log)
		return 99, nil
	}
	h.fake.On("ls --format json", "["+enginetest.ContainerJSON("web", "web", supervisedProject, "running", "10.0.0.2", "default", map[string]string{compose.LabelRestart: "on-failure"})+"]", 0)
	if code := h.run("run", "-d", "--name", "web", "--restart", "on-failure", "alpine"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if h.execd() != "" || !h.fake.Called("run --name web --detach --label com.apple-compose.restart=on-failure") {
		t.Fatalf("detached run with a policy must run in-process: %q %v", h.execd(), h.fake.Calls())
	}
	if len(spawned) != 1 || !strings.HasPrefix(spawned[0], supervisedProject+" ") {
		t.Fatalf("supervisor must be spawned for the pseudo project: %v", spawned)
	}
	// stop marks the container so the supervisor leaves it alone; start lifts it.
	if code := h.run("stop", "web"); code != 0 {
		t.Fatalf("stop: %d %s", code, h.err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("APPLE_COMPOSE_HOME"), "projects", supervisedProject, "stopped", "web")); err != nil {
		t.Fatalf("stop must leave a marker: %v", err)
	}
	if code := h.run("start", "web"); code != 0 {
		t.Fatalf("start: %d %s", code, h.err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("APPLE_COMPOSE_HOME"), "projects", supervisedProject, "stopped", "web")); err == nil {
		t.Fatal("start must clear the marker")
	}
	if code := h.run("run", "--restart", "always", "alpine"); code != 0 || h.execd() != "run --label com.apple-compose.restart=always --label com.docker.compose.project=apple-docker --label com.docker.compose.service=alpine --label com.docker.compose.oneoff=False alpine" {
		t.Fatalf("attached run with a policy must still exec: %q", h.execd())
	}
	if !strings.Contains(h.err.String(), "applies once the container runs detached") {
		t.Fatalf("expected attached-run warning:\n%s", h.err)
	}
}

func TestRunRejectsImpossibleOptions(t *testing.T) {
	h := newHarness(t)
	if h.run("run", "--network", "host", "alpine") == 0 || !strings.Contains(h.err.String(), "virtual machines") {
		t.Fatalf("host networking must fail:\n%s", h.err)
	}
	h.err.Reset()
	if h.run("run", "-p", "80", "alpine") == 0 || !strings.Contains(h.err.String(), "cannot allocate a host port") {
		t.Fatalf("ephemeral ports must fail:\n%s", h.err)
	}
	h.err.Reset()
	h.fake.On("image inspect", "Error: not found", 1)
	if h.run("run", "--pull", "never", "ghcr.io/x/missing") == 0 || !strings.Contains(h.err.String(), "--pull=never") {
		t.Fatalf("--pull never must fail on a missing image:\n%s", h.err)
	}
}

func TestRunPullAlwaysPullsFirst(t *testing.T) {
	h := newHarness(t)
	if code := h.run("run", "--pull", "always", "alpine", "true"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if !h.fake.Called("image pull alpine") || h.execd() != "run alpine true" {
		t.Fatalf("pull must precede run: %v / %q", h.fake.Calls(), h.execd())
	}
}

func TestCreatePrintsName(t *testing.T) {
	h := newHarness(t)
	h.fake.On("create", "web\n", 0)
	if code := h.run("create", "--name", "web", "alpine"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if got := h.fake.Call("create"); got != "create --name web alpine" {
		t.Fatalf("create = %q", got)
	}
	if strings.TrimSpace(h.out.String()) != "web" {
		t.Fatalf("create must print the id, got %q", h.out.String())
	}
}

func TestPsRendersDockerColumnsAndFormats(t *testing.T) {
	h := newHarness(t)
	listing := "[" + enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", map[string]string{"tier": "web"}) + "," +
		enginetest.ContainerJSON("old", "old", "p", "stopped", "", "default", nil) + "]"
	h.fake.On("ls --format json --all", listing, 0)
	h.fake.On("ls --format json", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", map[string]string{"tier": "web"})+"]", 0)
	if code := h.run("ps"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	out := h.out.String()
	if !strings.HasPrefix(out, "CONTAINER ID   IMAGE") || !strings.Contains(out, "0.0.0.0:8080->80/tcp") || strings.Contains(out, "old") {
		t.Fatalf("ps table:\n%s", out)
	}
	h.out.Reset()
	if code := h.run("ps", "-a", "--format", "{{.Names}}={{.State}}"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if got := strings.TrimSpace(h.out.String()); got != "web=running\nold=exited" && got != "old=exited\nweb=running" {
		t.Fatalf("templated ps = %q", got)
	}
	h.out.Reset()
	if code := h.run("ps", "-a", "--filter", "status=exited", "-q"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if got := strings.TrimSpace(h.out.String()); got != "old" {
		t.Fatalf("status filter = %q", got)
	}
	h.out.Reset()
	if code := h.run("ps", "--filter", "label=tier=web", "--format", "json"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if !strings.Contains(h.out.String(), `"Names":"web"`) || !strings.Contains(h.out.String(), `"Labels":"`) {
		t.Fatalf("json ps = %s", h.out)
	}
	h.out.Reset()
	if code := h.run("container", "ls", "--format", "table {{.Names}}\t{{.Image}}"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if !strings.HasPrefix(h.out.String(), "NAMES   IMAGE") {
		t.Fatalf("table template = %s", h.out)
	}
}

func TestLifecycleCommandsEchoNames(t *testing.T) {
	h := newHarness(t)
	h.fake.On("inspect web", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", nil)+"]", 0)
	h.fake.On("stop --time 10 nope", "Error: container nope not found", 1)
	if code := h.run("stop", "-t", "3", "web"); code != 0 || strings.TrimSpace(h.out.String()) != "web" {
		t.Fatalf("stop: code=%d out=%q err=%s", code, h.out, h.err)
	}
	if got := h.fake.Call("stop"); got != "stop --time 3 web" {
		t.Fatalf("stop = %q", got)
	}
	h.out.Reset()
	h.fake.Reset()
	if code := h.run("restart", "web"); code != 0 {
		t.Fatalf("restart: %d %s", code, h.err)
	}
	if !h.fake.Called("stop --time 10 web") || !h.fake.Called("start web") {
		t.Fatalf("restart must stop then start: %v", h.fake.Calls())
	}
	h.out.Reset()
	h.fake.Reset()
	if code := h.run("kill", "-s", "HUP", "web"); code != 0 || h.fake.Call("kill") != "kill --signal HUP web" {
		t.Fatalf("kill: %d %v", code, h.fake.Calls())
	}
	h.fake.Reset()
	if code := h.run("rm", "-f", "web"); code != 0 || h.fake.Call("delete") != "delete --force web" {
		t.Fatalf("rm: %d %v", code, h.fake.Calls())
	}
	h.out.Reset()
	h.err.Reset()
	if code := h.run("stop", "nope"); code != 1 || !strings.Contains(h.err.String(), "Error response from daemon") {
		t.Fatalf("a failing stop must exit 1 with Docker's error prefix: %d %s", code, h.err)
	}
	if code := h.run("start", "web", "db"); code != 0 || strings.TrimSpace(h.out.String()) != "web\ndb" {
		t.Fatalf("start must echo names: %d %q", code, h.out)
	}
	if code := h.run("start", "-ai", "web"); code != 0 || h.execd() != "start --attach --interactive web" {
		t.Fatalf("start -ai must attach: %q", h.execd())
	}
}

func TestExecAndLogsTranslate(t *testing.T) {
	h := newHarness(t)
	if code := h.run("exec", "-it", "-u", "app", "-w", "/srv", "-e", "X=1", "web", "sh", "-c", "ls -l"); code != 0 {
		t.Fatalf("exec: %d %s", code, h.err)
	}
	if got := h.execd(); got != "exec --interactive --tty --user app --workdir /srv --env X=1 web sh -c ls -l" {
		t.Fatalf("exec = %q", got)
	}
	if code := h.run("exec", "-d", "-it", "web", "true"); code != 0 || h.execd() != "exec --detach web true" {
		t.Fatalf("detached exec = %q", h.execd())
	}
	if code := h.run("logs", "-f", "--tail", "50", "web"); code != 0 || h.execd() != "logs --follow -n 50 web" {
		t.Fatalf("logs = %q", h.execd())
	}
	h.fake.On("logs web", "line one\nline two\n", 0)
	h.out.Reset()
	if code := h.run("logs", "-t", "web"); code != 0 {
		t.Fatalf("logs -t: %d %s", code, h.err)
	}
	if lines := strings.Split(strings.TrimSpace(h.out.String()), "\n"); len(lines) != 2 || !strings.HasSuffix(lines[0], " line one") || !strings.Contains(lines[0], "T") {
		t.Fatalf("stamped logs = %q", h.out.String())
	}
	if code := h.run("top", "web"); code != 0 || h.execd() != "exec web ps aux" {
		t.Fatalf("top = %q", h.execd())
	}
}

func TestInspectShapesDockerView(t *testing.T) {
	h := newHarness(t)
	h.fake.On("inspect web", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", map[string]string{"tier": "web"})+"]", 0)
	if code := h.run("inspect", "web"); code != 0 {
		t.Fatalf("inspect: %d %s", code, h.err)
	}
	out := h.out.String()
	for _, want := range []string{`"Id": "web"`, `"Name": "/web"`, `"Status": "running"`, `"Running": true`, `"IPAddress": "10.0.0.2"`, `"80/tcp"`, `"HostPort": "8080"`, `"tier": "web"`, `"Runtime"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspect lacks %s:\n%s", want, out)
		}
	}
	h.out.Reset()
	if code := h.run("inspect", "-f", "{{.State.Status}} {{.NetworkSettings.IPAddress}} {{index .Config.Labels \"tier\"}}", "web"); code != 0 {
		t.Fatalf("inspect -f: %d %s", code, h.err)
	}
	if got := strings.TrimSpace(h.out.String()); got != "running 10.0.0.2 web" {
		t.Fatalf("templated inspect = %q", got)
	}
	h.out.Reset()
	h.fake.On("inspect", "Error: not found", 1)
	h.fake.On("image inspect alpine", `[{"id":"sha256:abc","configuration":{"name":"docker.io/library/alpine:3.20","creationDate":"2026-09-01T00:00:00Z","descriptor":{"digest":"sha256:abc","size":10}},"variants":[{"platform":{"architecture":"arm64","os":"linux"},"size":5000000,"config":{"config":{"Cmd":["/bin/sh"],"Env":["PATH=/bin"]}}}]}]`, 0)
	if code := h.run("inspect", "alpine"); code != 0 {
		t.Fatalf("image inspect fallback: %d %s", code, h.err)
	}
	if !strings.Contains(h.out.String(), `"RepoTags"`) || !strings.Contains(h.out.String(), `"/bin/sh"`) {
		t.Fatalf("image view:\n%s", h.out)
	}
	h.out.Reset()
	h.err.Reset()
	h.fake.On("image inspect", "Error: not found", 1)
	h.fake.On("network inspect", "Error: not found", 1)
	h.fake.On("volume inspect", "Error: not found", 1)
	if code := h.run("inspect", "ghost"); code != 1 || !strings.Contains(h.err.String(), "No such object: ghost") {
		t.Fatalf("missing object: %d %s", code, h.err)
	}
}

func TestImagesAndImageCommands(t *testing.T) {
	h := newHarness(t)
	h.fake.On("image ls --format json", `[{"id":"sha256:0123456789abcdef0123","configuration":{"name":"docker.io/library/alpine:3.20","creationDate":"2026-09-01T00:00:00Z","descriptor":{"digest":"sha256:0123456789abcdef0123","size":10}},"variants":[{"size":5000000}]},{"id":"sha256:fedcba","configuration":{"name":"ghcr.io/x/app:v1","creationDate":"2026-09-02T00:00:00Z","descriptor":{"digest":"sha256:fedcba","size":10}},"variants":[{"size":2000}]}]`, 0)
	if code := h.run("images"); code != 0 {
		t.Fatalf("images: %d %s", code, h.err)
	}
	out := h.out.String()
	if !strings.HasPrefix(out, "REPOSITORY") || !strings.Contains(out, "alpine") || !strings.Contains(out, "3.20") || !strings.Contains(out, "0123456789ab") || !strings.Contains(out, "5MB") {
		t.Fatalf("images table:\n%s", out)
	}
	h.out.Reset()
	if code := h.run("images", "-q", "ghcr.io/x/app"); code != 0 || strings.TrimSpace(h.out.String()) != "fedcba" {
		t.Fatalf("images -q with reference = %q", h.out)
	}
	h.out.Reset()
	if code := h.run("image", "ls", "--filter", "reference=alpine*", "--format", "{{.Repository}}:{{.Tag}}"); code != 0 || strings.TrimSpace(h.out.String()) != "alpine:3.20" {
		t.Fatalf("reference glob = %q", h.out)
	}
	if code := h.run("pull", "--platform", "linux/amd64", "-q", "nginx"); code != 0 || h.execd() != "image pull --platform linux/amd64 --progress none nginx" {
		t.Fatalf("pull = %q", h.execd())
	}
	if code := h.run("push", "ghcr.io/x/app:v1"); code != 0 || h.execd() != "image push ghcr.io/x/app:v1" {
		t.Fatalf("push = %q", h.execd())
	}
	if code := h.run("tag", "alpine:3.20", "mine:latest"); code != 0 || h.fake.Call("image tag") != "image tag alpine:3.20 mine:latest" {
		t.Fatalf("tag = %v", h.fake.Calls())
	}
	h.out.Reset()
	if code := h.run("rmi", "-f", "mine:latest"); code != 0 || h.fake.Call("image delete") != "image delete --force mine:latest" || !strings.Contains(h.out.String(), "Untagged: mine:latest") {
		t.Fatalf("rmi = %v %q", h.fake.Calls(), h.out)
	}
	if code := h.run("build", "-t", "app:dev", "-f", "Dockerfile.dev", "--build-arg", "A=1", "--target", "prod", "--no-cache", "--platform", "linux/arm64,linux/amd64", "-q", "--cache-from", "x", "./app"); code != 0 {
		t.Fatalf("build: %d %s", code, h.err)
	}
	if got := h.execd(); got != "build --tag app:dev --file Dockerfile.dev --build-arg A=1 --platform linux/arm64 --platform linux/amd64 --target prod --no-cache --progress plain ./app" {
		t.Fatalf("build = %q", got)
	}
	if !strings.Contains(h.err.String(), "--cache-from has no equivalent") {
		t.Fatalf("expected ignored-flag warning:\n%s", h.err)
	}
	if code := h.run("build", "."); code != 0 || h.execd() != "build ." {
		t.Fatalf("bare build = %q", h.execd())
	}
}

func TestNetworkAndVolumeCommands(t *testing.T) {
	h := newHarness(t)
	h.fake.On("network ls --format json", `[{"id":"default","configuration":{"name":"default","mode":"nat","labels":{"com.apple.container.resource.role":"builtin"}},"status":{"ipv4Subnet":"192.168.65.0/24","ipv4Gateway":"192.168.65.1"}},{"id":"front","configuration":{"name":"front","mode":"nat","labels":{"tier":"web"}},"status":{}}]`, 0)
	if code := h.run("network", "ls"); code != 0 {
		t.Fatalf("network ls: %d %s", code, h.err)
	}
	if !strings.HasPrefix(h.out.String(), "NETWORK ID   NAME") || !strings.Contains(h.out.String(), "front") {
		t.Fatalf("network table:\n%s", h.out)
	}
	h.out.Reset()
	if code := h.run("network", "ls", "-q", "--filter", "label=tier"); code != 0 || strings.TrimSpace(h.out.String()) != "front" {
		t.Fatalf("network filter = %q", h.out)
	}
	h.out.Reset()
	if code := h.run("network", "create", "--subnet", "10.9.0.0/24", "--internal", "--label", "a=b", "-o", "k=v", "--gateway", "10.9.0.1", "back"); code != 0 {
		t.Fatalf("network create: %d %s", code, h.err)
	}
	if got := h.fake.Call("network create"); got != "network create --subnet 10.9.0.0/24 --internal --label a=b --option k=v back" {
		t.Fatalf("network create = %q", got)
	}
	if strings.TrimSpace(h.out.String()) != "back" || !strings.Contains(h.err.String(), "--gateway") {
		t.Fatalf("network create output = %q / %s", h.out, h.err)
	}
	h.out.Reset()
	if code := h.run("network", "rm", "back"); code != 0 || h.fake.Call("network delete") != "network delete back" || strings.TrimSpace(h.out.String()) != "back" {
		t.Fatalf("network rm = %v %q", h.fake.Calls(), h.out)
	}
	h.out.Reset()
	h.fake.On("network inspect front", `[{"id":"front","configuration":{"name":"front","mode":"nat","labels":{"tier":"web"},"creationDate":"2026-09-01T00:00:00Z"},"status":{"ipv4Subnet":"10.1.0.0/24","ipv4Gateway":"10.1.0.1"}}]`, 0)
	if code := h.run("network", "inspect", "-f", "{{range .IPAM.Config}}{{.Subnet}}{{end}} {{.Internal}}", "front"); code != 0 || strings.TrimSpace(h.out.String()) != "10.1.0.0/24 false" {
		t.Fatalf("network inspect = %q %s", h.out, h.err)
	}
	h.out.Reset()
	h.fake.On("ls --format json --all", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "front", nil)+"]", 0)
	h.fake.On("network ls --format json", `[{"id":"default","configuration":{"name":"default","labels":{"com.apple.container.resource.role":"builtin"}}},{"id":"front","configuration":{"name":"front"}},{"id":"unused","configuration":{"name":"unused"}}]`, 0)
	h.fake.Reset()
	if code := h.run("network", "prune", "-f"); code != 0 {
		t.Fatalf("network prune: %d %s", code, h.err)
	}
	if !h.fake.Called("network delete unused") || h.fake.Called("network delete front") || h.fake.Called("network delete default") {
		t.Fatalf("prune must remove only unused custom networks: %v", h.fake.Calls())
	}

	h.out.Reset()
	h.fake.On("volume ls --format json", `[{"id":"data","configuration":{"name":"data","driver":"local","labels":{"p":"x"},"sizeInBytes":1000000,"source":"/vols/data"}}]`, 0)
	if code := h.run("volume", "ls"); code != 0 || !strings.Contains(h.out.String(), "local    data") {
		t.Fatalf("volume ls = %q %s", h.out, h.err)
	}
	h.out.Reset()
	if code := h.run("volume", "create", "--label", "a=b", "--opt", "size=2g", "--opt", "x=y", "vol1"); code != 0 {
		t.Fatalf("volume create: %d %s", code, h.err)
	}
	if got := h.fake.Call("volume create"); got != "volume create --label a=b -s 2g --opt x=y vol1" {
		t.Fatalf("volume create = %q", got)
	}
	h.out.Reset()
	if code := h.run("volume", "create"); code != 0 || len(strings.TrimSpace(h.out.String())) != 64 {
		t.Fatalf("anonymous volume must get a 64-hex name: %q", h.out)
	}
	if code := h.run("volume", "rm", "vol1"); code != 0 || h.fake.Call("volume delete") != "volume delete vol1" {
		t.Fatalf("volume rm = %v", h.fake.Calls())
	}
	h.out.Reset()
	h.fake.On("volume inspect data", `[{"id":"data","configuration":{"name":"data","source":"/vols/data","labels":{"p":"x"}}}]`, 0)
	if code := h.run("volume", "inspect", "-f", "{{.Mountpoint}} {{.Driver}}", "data"); code != 0 || strings.TrimSpace(h.out.String()) != "/vols/data local" {
		t.Fatalf("volume inspect = %q %s", h.out, h.err)
	}
}

func TestPruneAsksForConfirmation(t *testing.T) {
	h := newHarness(t)
	h.app.in = strings.NewReader("n\n")
	if code := h.run("system", "prune"); code != 0 || h.fake.Called("prune") {
		t.Fatalf("declined prune must do nothing: %d %v", code, h.fake.Calls())
	}
	h.app.in = strings.NewReader("y\n")
	h.fake.On("network ls --format json", "[]", 0)
	h.fake.On("ls --format json --all", "[]", 0)
	if code := h.run("system", "prune", "-a", "--volumes"); code != 0 {
		t.Fatalf("system prune: %d %s", code, h.err)
	}
	calls := strings.Join(h.fake.Calls(), "\n")
	for _, want := range []string{"prune", "image prune --all", "volume prune"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("system prune must run %q:\n%s", want, calls)
		}
	}
	if code := h.run("image", "prune", "-f"); code != 0 || !h.fake.Called("image prune") {
		t.Fatalf("image prune -f: %d %v", code, h.fake.Calls())
	}
	if code := h.run("container", "prune", "-f"); code != 0 {
		t.Fatalf("container prune -f: %d %s", code, h.err)
	}
}

func TestUnsupportedCommandsExplain(t *testing.T) {
	h := newHarness(t)
	for _, c := range [][]string{{"pause", "web"}, {"rename", "a", "b"}, {"commit", "web"}, {"events"}, {"network", "connect", "n", "c"}} {
		h.err.Reset()
		if code := h.run(c...); code == 0 || !strings.Contains(h.err.String(), "not supported") {
			t.Fatalf("%v must fail with an explanation: %d %s", c, code, h.err)
		}
	}
	h.fake.On("inspect web", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", nil)+"]", 0)
	h.err.Reset()
	if code := h.run("attach", "web"); code == 0 || !strings.Contains(h.err.String(), "logs -f") {
		t.Fatalf("attach to a running container must point at logs/exec: %s", h.err)
	}
}

func TestVersionInfoAndWait(t *testing.T) {
	h := newHarness(t)
	h.fake.On("--version", "container CLI version 1.3.1\n", 0)
	if code := h.run("version"); code != 0 || !strings.Contains(h.out.String(), "Version:    test") {
		t.Fatalf("version: %d %q", code, h.out)
	}
	h.out.Reset()
	h.fake.On("ls --format json --all", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", nil)+"]", 0)
	h.fake.On("image ls --format json", "[]", 0)
	if code := h.run("info", "--format", "{{.ContainersRunning}}/{{.Containers}}"); code != 0 || strings.TrimSpace(h.out.String()) != "1/1" {
		t.Fatalf("info = %q %s", h.out, h.err)
	}
	h.out.Reset()
	h.fake.On("inspect web", "["+enginetest.ContainerJSON("web", "web", "p", "stopped", "", "default", nil)+"]", 0)
	if code := h.run("wait", "web"); code != 0 || strings.TrimSpace(h.out.String()) != "0" {
		t.Fatalf("wait = %q %s", h.out, h.err)
	}
	h.out.Reset()
	h.fake.On("inspect web", "["+enginetest.ContainerJSON("web", "web", "p", "running", "", "default", nil)+"]", 0)
	if code := h.run("port", "web", "80"); code != 0 || strings.TrimSpace(h.out.String()) != "0.0.0.0:8080" {
		t.Fatalf("port = %q", h.out)
	}
	h.out.Reset()
	if code := h.run("port", "web"); code != 0 || strings.TrimSpace(h.out.String()) != "80/tcp -> 0.0.0.0:8080" {
		t.Fatalf("port listing = %q", h.out)
	}
}

func TestGlobalFlagsAndDryRun(t *testing.T) {
	h := newHarness(t)
	h.app.eng.DryRun = true
	if code := h.run("-H", "unix:///var/run/docker.sock", "--context", "default", "run", "alpine"); code != 0 {
		t.Fatalf("docker globals must be accepted: %d %s", code, h.err)
	}
	if !strings.Contains(h.err.String(), "[dry-run] container run alpine") || h.execd() != "" {
		t.Fatalf("dry run must print instead of exec: %s / %q", h.err, h.execd())
	}
	if code := h.run("compose", "--help"); code != 0 {
		t.Fatalf("compose delegation: %d %s", code, h.err)
	}
}

func TestCleanPassesThrough(t *testing.T) {
	h := newHarness(t)
	if code := h.run("container", "clean", "web", "db"); code != 0 || h.fake.Call("clean") != "clean web db" {
		t.Fatalf("container clean = %d %v", code, h.fake.Calls())
	}
	h.fake.Reset()
	h.fake.On("ls --format json", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", nil)+","+enginetest.ContainerJSON("buildkit", "", "", "running", "10.0.0.9", "default", map[string]string{"com.apple.container.resource.role": "builder"})+"]", 0)
	if code := h.run("system", "clean"); code != 0 || h.fake.Call("clean") != "clean web" {
		t.Fatalf("system clean must target running containers: %d %v", code, h.fake.Calls())
	}
}

func TestFormatUnescapesDockerSequences(t *testing.T) {
	h := newHarness(t)
	h.fake.On("ls --format json", "["+enginetest.ContainerJSON("web", "web", "p", "running", "10.0.0.2", "default", nil)+"]", 0)
	if code := h.run("ps", "--format", `{{.Names}}\t{{.State}}\n`); code != 0 {
		t.Fatalf("exit %d:\n%s", code, h.err)
	}
	if got := h.out.String(); got != "web\trunning\n\n" {
		t.Fatalf("escapes must become tab and newline, got %q", got)
	}
}

func TestFormatHelpers(t *testing.T) {
	if humanSize(999) != "999B" || humanSize(1500) != "1.5kB" || humanSize(21_400_000) != "21.4MB" || humanSize(3_500_000_000) != "3.5GB" {
		t.Fatalf("humanSize: %s %s %s %s", humanSize(999), humanSize(1500), humanSize(21_400_000), humanSize(3_500_000_000))
	}
	if shortID("sha256:0123456789abcdef", false) != "0123456789ab" || shortID("sha256:0123456789abcdef", true) != "0123456789abcdef" {
		t.Fatal("shortID")
	}
	if !globMatch("alpine*", "alpine:3.20") || globMatch("nginx*", "alpine") || !globMatch("*app*", "ghcr.io/x/app:v1") {
		t.Fatal("globMatch")
	}
	fl, err := parseFilters([]string{"name=web", "label=a=b"})
	if err != nil || fl["name"][0] != "web" || fl["label"][0] != "a=b" {
		t.Fatal("parseFilters")
	}
	if _, err := parseFilters([]string{"bogus"}); err == nil {
		t.Fatal("bad filter must fail")
	}
}

func TestDefaultDNSAppliesToRunAndBuild(t *testing.T) {
	h := newHarness(t)
	t.Setenv("APPLE_COMPOSE_DNS", "1.1.1.1, 8.8.8.8")
	if code := h.run("run", "alpine"); code != 0 || h.execd() != "run --dns 1.1.1.1 --dns 8.8.8.8 alpine" {
		t.Fatalf("run must carry the default nameservers: %q", h.execd())
	}
	if code := h.run("run", "--dns", "9.9.9.9", "alpine"); code != 0 || h.execd() != "run --dns 9.9.9.9 alpine" {
		t.Fatalf("an explicit --dns must win: %q", h.execd())
	}
	if code := h.run("build", "."); code != 0 || h.execd() != "build --dns 1.1.1.1 --dns 8.8.8.8 ." {
		t.Fatalf("build must carry the default nameservers: %q", h.execd())
	}
}
