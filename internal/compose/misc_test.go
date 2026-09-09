package compose

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skuirrels/apple-compose/internal/enginetest"
	"github.com/skuirrels/apple-compose/internal/project"
)

const twoServices = `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
    depends_on: [db]
  db:
    image: docker.io/library/alpine:3.20
`

func TestStartOnlyStartsStoppedContainersInOrder(t *testing.T) {
	r, f, errBuf := upFixture(t, twoServices, "t-db-1", "t-web-1")
	f.On("ls --format json --all", "["+
		stopped("t-web-1", "web", "t", nil)+","+
		stopped("t-db-1", "db", "t", nil)+","+
		running("t-db-2", "db", "t", map[string]string{project.LabelContainerNumber: "2"})+
		"]", 0)
	if err := r.Start(context.Background(), nil); err != nil {
		t.Fatalf("start: %v\n%s", err, errBuf)
	}
	calls := f.Calls()
	db, web := indexOf(calls, "start t-db-1"), indexOf(calls, "start t-web-1")
	if db < 0 || web < 0 || db > web {
		t.Fatalf("db must start before web:\n%s", strings.Join(calls, "\n"))
	}
	if f.Called("start t-db-2") || !strings.Contains(errBuf.String(), "t-db-2  Running") {
		t.Fatalf("running containers must be reported, not restarted:\n%s", errBuf)
	}
}

func TestStartWithoutContainersFails(t *testing.T) {
	r, _, _ := upFixture(t, twoServices)
	err := r.Start(context.Background(), []string{"web"})
	if err == nil || !strings.Contains(err.Error(), "run `up` first") {
		t.Fatalf("expected hint to run up, got %v", err)
	}
}

func TestStopUsesReverseOrderAndTimeout(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json", "["+running("t-db-1", "db", "t", nil)+","+running("t-web-1", "web", "t", nil)+"]", 0)
	if err := r.Stop(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("stop"); got != "stop --time 10 t-web-1 t-db-1" {
		t.Fatalf("stop = %q", got)
	}
	f.Reset()
	if err := r.Stop(context.Background(), []string{"db"}, 3e9); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("stop"); got != "stop --time 3 t-db-1" {
		t.Fatalf("stop with timeout = %q", got)
	}
}

func TestRestartIncludesDependentsUnlessNoDeps(t *testing.T) {
	r, f, _ := upFixture(t, twoServices, "t-db-1", "t-web-1")
	all := "[" + running("t-db-1", "db", "t", nil) + "," + running("t-web-1", "web", "t", nil) + "]"
	f.On("ls --format json", all, 0)
	if err := r.Restart(context.Background(), []string{"db"}, 0, false); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("stop"); got != "stop --time 10 t-web-1 t-db-1" {
		t.Fatalf("restart must stop dependents too: %q", got)
	}
	f.Reset()
	if err := r.Restart(context.Background(), []string{"db"}, 0, true); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("stop"); got != "stop --time 10 t-db-1" {
		t.Fatalf("--no-deps must limit the restart: %q", got)
	}
}

func TestRmSkipsRunningUnlessForcedOrStopped(t *testing.T) {
	r, f, errBuf := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+running("t-db-1", "db", "t", nil)+","+stopped("t-web-1", "web", "t", nil)+"]", 0)
	if err := r.Rm(context.Background(), nil, false, false, true); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("delete"); got != "delete --force t-web-1" {
		t.Fatalf("only the stopped container may be removed: %q", got)
	}
	if !strings.Contains(errBuf.String(), "t-db-1 is running") || !strings.Contains(errBuf.String(), "anonymous volumes") {
		t.Fatalf("expected warnings:\n%s", errBuf)
	}
	f.Reset()
	if err := r.Rm(context.Background(), nil, false, true, false); err != nil {
		t.Fatal(err)
	}
	if !f.Called("stop --time 10 t-db-1") || f.Call("delete") != "delete --force t-web-1 t-db-1" {
		t.Fatalf("--stop must stop then remove everything: %v", f.Calls())
	}
}

func TestKillSignalsRunningContainers(t *testing.T) {
	r, f, errBuf := upFixture(t, twoServices)
	f.On("ls --format json", "["+running("t-db-1", "db", "t", nil)+","+running("t-web-1", "web", "t", nil)+"]", 0)
	if err := r.Kill(context.Background(), []string{"web"}, "SIGUSR1"); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("kill"); got != "kill --signal SIGUSR1 t-web-1" {
		t.Fatalf("kill = %q", got)
	}
	if !strings.Contains(errBuf.String(), "t-web-1  Killed") {
		t.Fatalf("expected Killed step:\n%s", errBuf)
	}
}

func TestPortReportsPublishedBinding(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+running("t-web-1", "web", "t", nil)+"]", 0)
	got, err := r.Port(context.Background(), "web", 0, 80, "")
	if err != nil || got != "0.0.0.0:8080" {
		t.Fatalf("port = %q, %v", got, err)
	}
	if _, err := r.Port(context.Background(), "web", 0, 443, "tcp"); err == nil || !strings.Contains(err.Error(), "no published port 443/tcp") {
		t.Fatalf("expected missing port error, got %v", err)
	}
	if _, err := r.Port(context.Background(), "web", 2, 80, ""); err == nil || !strings.Contains(err.Error(), "no container with index 2") {
		t.Fatalf("expected index error, got %v", err)
	}
	if _, err := r.Port(context.Background(), "db", 0, 80, ""); err == nil || !strings.Contains(err.Error(), `service "db" has no container`) {
		t.Fatalf("expected no container error, got %v", err)
	}
}

func TestWaitReturnsRecordedExitCode(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+stopped("t-db-1", "db", "t", nil)+","+stopped("t-web-1", "web", "t", nil)+"]", 0)
	r.recordExit("t-web-1", 4)
	code, err := r.Wait(context.Background(), nil, false)
	if err != nil || code != 4 {
		t.Fatalf("wait = %d, %v", code, err)
	}
	out := r.Console.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, `"t-web-1" exited with status code 4`) || !strings.Contains(out, `"t-db-1" exited with status code 0`) {
		t.Fatalf("unexpected wait output:\n%s", out)
	}
}

func TestWaitDownProjectTearsDown(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+stopped("t-db-1", "db", "t", nil)+"]", 0)
	f.On("network ls --format json", "[]", 0)
	if code, err := r.Wait(context.Background(), nil, true); err != nil || code != 0 {
		t.Fatalf("wait = %d, %v", code, err)
	}
	if !f.Called("delete --force t-db-1") {
		t.Fatalf("--down must remove the project: %v", f.Calls())
	}
}

func TestImagesFormats(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+running("t-db-1", "db", "t", nil)+","+running("t-web-1", "web", "t", nil)+"]", 0)
	f.On("image ls --format json", `[{"id":"sha256:0123456789abcdef","configuration":{"name":"docker.io/library/alpine:3.20"},"variants":[{"size":2048},{"size":1024}]}]`, 0)
	out := r.Console.Out.(*bytes.Buffer)
	if err := r.Images(context.Background(), nil, false, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "t-db-1") || !strings.Contains(out.String(), "alpine") || !strings.Contains(out.String(), "3.0KB") {
		t.Fatalf("table output:\n%s", out)
	}
	out.Reset()
	if err := r.Images(context.Background(), []string{"web"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "0123456789ab" {
		t.Fatalf("quiet output = %q", got)
	}
	out.Reset()
	if err := r.Images(context.Background(), nil, false, "json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"Repository": "alpine"`) || !strings.Contains(out.String(), `"Tag": "3.20"`) {
		t.Fatalf("json output:\n%s", out)
	}
}

func TestSplitRefAndHumanBytes(t *testing.T) {
	for _, tc := range []struct{ ref, repo, tag string }{
		{"alpine", "alpine", "latest"},
		{"alpine:3.20", "alpine", "3.20"},
		{"localhost:5000/app", "localhost:5000/app", "latest"},
		{"ghcr.io/x/app:v1", "ghcr.io/x/app", "v1"},
		{"app@sha256:abc", "app", "sha256:abc"},
	} {
		repo, tag := splitRef(tc.ref)
		if repo != tc.repo || tag != tc.tag {
			t.Errorf("splitRef(%q) = %q,%q want %q,%q", tc.ref, repo, tag, tc.repo, tc.tag)
		}
	}
	for _, tc := range []struct {
		n    int64
		want string
	}{{0, "0B"}, {1023, "1023B"}, {1536, "1.5KB"}, {5 << 20, "5.0MB"}, {3 << 30, "3.0GB"}} {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q want %q", tc.n, got, tc.want)
		}
	}
}

func TestCopyResolvesServiceNames(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+running("t-web-1", "web", "t", nil)+"]", 0)
	if err := r.Copy(context.Background(), "./local.txt", "web:/tmp/remote.txt", 0); err != nil {
		t.Fatal(err)
	}
	if got := f.Call("cp"); got != "cp ./local.txt t-web-1:/tmp/remote.txt" {
		t.Fatalf("cp = %q", got)
	}
	if err := r.Copy(context.Background(), "db:/x", "/y", 0); err == nil {
		t.Fatal("copy from a service without containers must fail")
	}
}

func TestListProjectsSummarisesByLabel(t *testing.T) {
	f := enginetest.New(t)
	f.On("ls --format json --all", "["+
		running("a-web-1", "web", "a", map[string]string{project.LabelConfigFiles: "/a/compose.yaml"})+","+
		stopped("a-db-1", "db", "a", nil)+","+
		stopped("b-x-1", "x", "b", nil)+","+
		`{"id":"plain","configuration":{"labels":{}},"status":{"state":"running"}}`+
		"]", 0)
	got, err := ListProjects(context.Background(), f.Engine, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "a" || got[0].Status != "running(1), exited(1)" || got[0].ConfigFiles != "/a/compose.yaml" {
		t.Fatalf("running projects = %+v", got)
	}
	all, err := ListProjects(context.Background(), f.Engine, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[1].Name != "b" || all[1].Status != "exited(1)" {
		t.Fatalf("all projects = %+v", all)
	}
}

func TestCheckHealthStates(t *testing.T) {
	r, f, _ := upFixture(t, `
name: t
services:
  a:
    image: docker.io/library/alpine:3.20
    healthcheck:
      test: ["CMD-SHELL", "curl -f localhost"]
      start_period: 1h
  plain:
    image: docker.io/library/alpine:3.20
`)
	a, _ := r.Project.GetService("a")
	plain, _ := r.Project.GetService("plain")
	cs := toContainers([]engineContainer{{id: "t-a-1", labels: map[string]string{project.LabelService: "a"}}})
	c := &cs[0]
	c.Status.StartedDate = time.Now()
	ctx := context.Background()
	if got := r.CheckHealth(ctx, c, plain); got != HealthNone {
		t.Fatalf("no healthcheck must report none, got %q", got)
	}
	if got := r.CheckHealth(ctx, c, a); got != HealthHealthy {
		t.Fatalf("passing probe must report healthy, got %q", got)
	}
	if got := f.Call("exec"); got != "exec t-a-1 /bin/sh -c curl -f localhost" {
		t.Fatalf("probe command = %q", got)
	}
	f.On("exec t-a-1", "", 1)
	if got := r.CheckHealth(ctx, c, a); got != HealthStarting {
		t.Fatalf("failing probe inside start_period must report starting, got %q", got)
	}
	a.HealthCheck.StartPeriod = nil
	if got := r.CheckHealth(ctx, c, a); got != HealthUnhealthy {
		t.Fatalf("failing probe must report unhealthy, got %q", got)
	}
	c.Status.State = "stopped"
	if got := r.CheckHealth(ctx, c, a); got != HealthNone {
		t.Fatalf("stopped container must report none, got %q", got)
	}
}

func TestRecordedExitRoundTrip(t *testing.T) {
	r, _ := loadRunner(t, "name: t\nservices:\n  a:\n    image: img\n", nil)
	if _, ok := r.recordedExit("t-a-1"); ok {
		t.Fatal("no exit recorded yet")
	}
	r.recordExit("t-a-1", 7)
	if code, ok := r.recordedExit("t-a-1"); !ok || code != 7 {
		t.Fatalf("recorded exit = %d %v", code, ok)
	}
	home := os.Getenv("APPLE_COMPOSE_HOME")
	if err := os.WriteFile(filepath.Join(home, "projects", "t", "exit", "t-a-1"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.recordedExit("t-a-1"); ok {
		t.Fatal("unparseable exit file must be ignored")
	}
}

func TestStepOnceAndTrackedExit(t *testing.T) {
	r, errBuf := loadRunner(t, "name: t\nservices:\n  a:\n    image: img\n", nil)
	r.stepOnce("k", "Container", "x", "Healthy")
	r.stepOnce("k", "Container", "x", "Healthy")
	if strings.Count(errBuf.String(), "Healthy") != 1 {
		t.Fatalf("step must print once:\n%s", errBuf)
	}
	if _, ok := r.trackedExit("x"); ok {
		t.Fatal("nothing tracked yet")
	}
	ch := r.trackExit("x")
	got, ok := r.trackedExit("x")
	if !ok || got != ch {
		t.Fatal("tracked channel must be returned")
	}
	if joinNonEmpty("a", "", "b") != "a b" {
		t.Fatal("joinNonEmpty must skip blanks")
	}
}
