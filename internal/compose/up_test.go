package compose

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/enginetest"
	"github.com/skuirrels/apple-compose/internal/project"
)

const imageJSON = `[{"id":"sha256:abc","configuration":{"name":"docker.io/library/alpine:3.20"}}]`

// upFixture wires a project to the fake runtime with the responses every Up
// needs: an image that exists, a network that does not, and inspect answers
// that report each named container running with an address.
func upFixture(t *testing.T, yaml string, names ...string) (*Runner, *enginetest.Fake, *bytes.Buffer) {
	t.Helper()
	r, errBuf := loadRunner(t, yaml, nil)
	f := withFake(t, r)
	f.On("image inspect", imageJSON, 0)
	f.On("network inspect", "Error: not found", 1)
	f.On("ls --format json", "[]", 0)
	for _, n := range names {
		svc := n[strings.Index(n, "-")+1 : strings.LastIndex(n, "-")]
		f.On("inspect "+n, "["+enginetest.ContainerJSON(n, svc, "t", "running", "10.0.0.5", "t_default", nil)+"]", 0)
	}
	return r, f, errBuf
}

func hashOf(t *testing.T, r *Runner, service string) string {
	t.Helper()
	s, err := r.Project.GetService(service)
	if err != nil {
		t.Fatal(err)
	}
	h, err := ServiceHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func running(id, service, project string, labels map[string]string) string {
	return enginetest.ContainerJSON(id, service, project, "running", "10.0.0.7", project+"_default", labels)
}

func stopped(id, service, project string, labels map[string]string) string {
	return enginetest.ContainerJSON(id, service, project, "stopped", "", project+"_default", labels)
}

func indexOf(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func TestUpFreshProjectCreatesInDependencyOrder(t *testing.T) {
	r, f, errBuf := upFixture(t, `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
    depends_on: [db]
  db:
    image: docker.io/library/alpine:3.20
`, "t-db-1", "t-web-1")
	// The first listing is the pre-flight inventory; later ones show what
	// has been created so dependency checks and hosts files see it.
	f.OnSequence("ls --format json --all", "[]", "["+running("t-db-1", "db", "t", nil)+","+running("t-web-1", "web", "t", nil)+"]")
	code, err := r.Up(context.Background(), UpOptions{Detach: true})
	if err != nil || code != 0 {
		t.Fatalf("up: code=%d err=%v\n%s", code, err, errBuf)
	}
	calls := f.Calls()
	if idx := indexOf(calls, "network create"); idx < 0 || !strings.HasSuffix(calls[idx], " t_default") {
		t.Fatalf("default network must be created: %v", calls)
	}
	createDB, createWeb := indexOf(calls, "create --name t-db-1"), indexOf(calls, "create --name t-web-1")
	startDB, startWeb := indexOf(calls, "start t-db-1"), indexOf(calls, "start t-web-1")
	if createDB < 0 || createWeb < 0 || startDB < 0 || startWeb < 0 {
		t.Fatalf("both services must be created and started:\n%s", strings.Join(calls, "\n"))
	}
	if !(createDB < startDB && startDB < createWeb && createWeb < startWeb) {
		t.Fatalf("db must be running before web is created:\n%s", strings.Join(calls, "\n"))
	}
	if !strings.Contains(calls[createWeb], "--label "+project.LabelConfigHash+"="+hashOf(t, r, "web")) {
		t.Fatalf("config hash label missing: %s", calls[createWeb])
	}
	if out := errBuf.String(); strings.Count(out, "Started") != 2 || !strings.Contains(out, "Network t_default  Created") {
		t.Fatalf("unexpected progress output:\n%s", out)
	}
}

func TestUpKeepsRunningContainerWithSameConfig(t *testing.T) {
	r, f, errBuf := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	f.On("ls --format json --all", "["+running("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: hashOf(t, r, "a")})+"]", 0)
	if code, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil || code != 0 {
		t.Fatalf("up: code=%d err=%v\n%s", code, err, errBuf)
	}
	for _, verb := range []string{"create", "start", "delete", "stop"} {
		if f.Called(verb) {
			t.Fatalf("%s must not be called for an up-to-date container: %v", verb, f.Calls())
		}
	}
	if !strings.Contains(errBuf.String(), "t-a-1  Running") {
		t.Fatalf("expected Running step:\n%s", errBuf)
	}
}

func TestUpRecreatesWhenConfigChanges(t *testing.T) {
	r, f, errBuf := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	f.On("ls --format json --all", "["+running("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: "stale"})+"]", 0)
	if code, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil || code != 0 {
		t.Fatalf("up: code=%d err=%v\n%s", code, err, errBuf)
	}
	calls := f.Calls()
	stop, del, create, start := indexOf(calls, "stop --time 10 t-a-1"), indexOf(calls, "delete --force t-a-1"), indexOf(calls, "create --name t-a-1"), indexOf(calls, "start t-a-1")
	if !(stop >= 0 && stop < del && del < create && create < start) {
		t.Fatalf("expected stop, delete, create, start in order:\n%s", strings.Join(calls, "\n"))
	}
	if !strings.Contains(errBuf.String(), "t-a-1  Recreated") {
		t.Fatalf("expected Recreated step:\n%s", errBuf)
	}
}

func TestUpNoRecreateKeepsStaleContainer(t *testing.T) {
	r, f, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	f.On("ls --format json --all", "["+running("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: "stale"})+"]", 0)
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, NoRecreate: true}); err != nil {
		t.Fatal(err)
	}
	if f.Called("delete") || f.Called("create") {
		t.Fatalf("--no-recreate must leave the container alone: %v", f.Calls())
	}
}

func TestUpForceRecreateReplacesMatchingContainer(t *testing.T) {
	r, f, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	f.On("ls --format json --all", "["+running("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: hashOf(t, r, "a")})+"]", 0)
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, ForceRecreate: true}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("delete --force t-a-1") || !f.Called("create --name t-a-1") {
		t.Fatalf("--force-recreate must delete and create: %v", f.Calls())
	}
}

func TestUpStartsStoppedContainer(t *testing.T) {
	r, f, errBuf := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	f.On("ls --format json --all", "["+stopped("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: hashOf(t, r, "a")})+"]", 0)
	if _, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil {
		t.Fatal(err)
	}
	if f.Called("create") || !f.Called("start t-a-1") {
		t.Fatalf("a stopped container must be started, not created: %v", f.Calls())
	}
	if !strings.Contains(errBuf.String(), "t-a-1  Started") {
		t.Fatalf("expected Started step:\n%s", errBuf)
	}
}

func TestUpNoStartOnlyCreates(t *testing.T) {
	r, f, errBuf := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, NoStart: true}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("create --name t-a-1") || f.Called("start") {
		t.Fatalf("--no-start must create without starting: %v", f.Calls())
	}
	if !strings.Contains(errBuf.String(), "t-a-1  Created") {
		t.Fatalf("expected Created step:\n%s", errBuf)
	}
}

func TestUpAlwaysRecreateDepsCascades(t *testing.T) {
	r, f, _ := upFixture(t, `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
    depends_on: [db]
  db:
    image: docker.io/library/alpine:3.20
`, "t-db-1", "t-web-1")
	f.On("ls --format json --all", "["+
		running("t-db-1", "db", "t", map[string]string{project.LabelConfigHash: "stale"})+","+
		running("t-web-1", "web", "t", map[string]string{project.LabelConfigHash: hashOf(t, r, "web")})+
		"]", 0)
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, AlwaysRecreateDeps: true}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("delete --force t-db-1") || !f.Called("delete --force t-web-1") {
		t.Fatalf("a recreated dependency must recreate its dependents: %v", f.Calls())
	}
}

func TestUpRetiresSurplusReplicas(t *testing.T) {
	r, f, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	h := hashOf(t, r, "a")
	f.On("ls --format json --all", "["+
		running("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: h})+","+
		running("t-a-2", "a", "t", map[string]string{project.LabelConfigHash: h, project.LabelContainerNumber: "2"})+
		"]", 0)
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, Scale: map[string]int{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.Calls(), "\n")
	if !strings.Contains(joined, "stop --time 10 t-a-2") || !strings.Contains(joined, "delete --force t-a-2") {
		t.Fatalf("replica 2 must be retired:\n%s", joined)
	}
	if strings.Contains(joined, "t-a-1\n") && (f.Called("delete --force t-a-1") || f.Called("create")) {
		t.Fatalf("replica 1 must be kept:\n%s", joined)
	}
}

func TestUpScalesOut(t *testing.T) {
	r, f, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1", "t-a-2", "t-a-3")
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, Scale: map[string]int{"a": 3}}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"t-a-1", "t-a-2", "t-a-3"} {
		if c := f.Call("create --name " + n); !strings.Contains(c, "--label "+project.LabelContainerNumber+"="+n[len(n)-1:]) {
			t.Fatalf("replica %s must carry its number: %q", n, c)
		}
	}
}

func TestUpRejectsContainerNameWithReplicas(t *testing.T) {
	r, _, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n    container_name: fixed\n")
	_, err := r.Up(context.Background(), UpOptions{Detach: true, Scale: map[string]int{"a": 2}})
	if err == nil || !strings.Contains(err.Error(), "container_name cannot be used with more than one replica") {
		t.Fatalf("expected replica error, got %v", err)
	}
}

func TestUpRejectsUnknownExitCodeFrom(t *testing.T) {
	r, _, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n")
	_, err := r.Up(context.Background(), UpOptions{ExitCodeFrom: "nope"})
	if err == nil || !strings.Contains(err.Error(), "--exit-code-from") {
		t.Fatalf("expected --exit-code-from error, got %v", err)
	}
}

func TestUpFailsWhenRuntimeIsDown(t *testing.T) {
	r, f, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n")
	f.On("system status", "Error: apiserver is not running", 1)
	_, err := r.Up(context.Background(), UpOptions{Detach: true})
	if err == nil || !strings.Contains(err.Error(), "container system start") {
		t.Fatalf("expected runtime hint, got %v", err)
	}
}

func TestUpReportsOrphansAndRemovesThemOnRequest(t *testing.T) {
	yaml := "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n"
	r, f, errBuf := upFixture(t, yaml, "t-a-1")
	listing := "[" + running("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: hashOf(t, r, "a")}) + "," + running("t-gone-1", "gone", "t", nil) + "]"
	f.On("ls --format json --all", listing, 0)
	if _, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "orphan containers ([t-gone-1])") || f.Called("delete") {
		t.Fatalf("orphans must be reported, not removed:\n%s\n%v", errBuf, f.Calls())
	}
	f.Reset()
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, RemoveOrphans: true}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("stop --time 10 t-gone-1") || !f.Called("delete --force t-gone-1") {
		t.Fatalf("--remove-orphans must stop and delete: %v", f.Calls())
	}
}

func TestUpDryRunMutatesNothing(t *testing.T) {
	r, f, _ := upFixture(t, `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
    depends_on: [db]
  db:
    image: docker.io/library/alpine:3.20
volumes:
  data: {}
`)
	f.On("volume inspect", "Error: not found", 1)
	r.Engine.DryRun = true
	echo := &bytes.Buffer{}
	r.Engine.Log = echo
	if code, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil || code != 0 {
		t.Fatalf("dry run: code=%d err=%v", code, err)
	}
	for _, want := range []string{"[dry-run] container network create", "[dry-run] container volume create", "[dry-run] container create --name t-db-1", "[dry-run] container start t-web-1"} {
		if !strings.Contains(echo.String(), want) {
			t.Fatalf("dry run must echo %q:\n%s", want, echo)
		}
	}
	for _, verb := range []string{"create", "start", "network create", "volume create", "delete"} {
		if f.Called(verb) {
			t.Fatalf("dry run must not run %q: %v", verb, f.Calls())
		}
	}
}

func TestUpWaitsForHealthyDependency(t *testing.T) {
	r, f, errBuf := upFixture(t, `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
    depends_on:
      db:
        condition: service_healthy
  db:
    image: docker.io/library/alpine:3.20
    healthcheck:
      test: ["CMD", "true"]
`, "t-db-1", "t-web-1")
	f.OnSequence("ls --format json --all", "[]", "["+running("t-db-1", "db", "t", nil)+"]")
	if _, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil {
		t.Fatalf("up: %v\n%s", err, errBuf)
	}
	calls := f.Calls()
	probe, createWeb := indexOf(calls, "exec t-db-1 true"), indexOf(calls, "create --name t-web-1")
	if probe < 0 || createWeb < probe {
		t.Fatalf("web must wait for the db probe:\n%s", strings.Join(calls, "\n"))
	}
	if !strings.Contains(errBuf.String(), "t-db-1  Healthy") {
		t.Fatalf("expected Healthy step:\n%s", errBuf)
	}
}

func TestUpFailsFastOnUnhealthyDependency(t *testing.T) {
	r, f, _ := upFixture(t, `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
    depends_on:
      db:
        condition: service_healthy
  db:
    image: docker.io/library/alpine:3.20
    healthcheck:
      test: ["CMD", "false"]
      retries: 1
      interval: 1ms
`, "t-db-1", "t-web-1")
	f.OnSequence("ls --format json --all", "[]", "["+running("t-db-1", "db", "t", nil)+"]")
	f.On("exec t-db-1 false", "", 1)
	_, err := r.Up(context.Background(), UpOptions{Detach: true})
	if err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("expected unhealthy error, got %v", err)
	}
	if f.Called("create --name t-web-1") {
		t.Fatalf("web must not be created after its dependency fails")
	}
}

func TestUpHealthyConditionNeedsHealthcheck(t *testing.T) {
	r, f, _ := upFixture(t, `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
    depends_on:
      db:
        condition: service_healthy
  db:
    image: docker.io/library/alpine:3.20
`, "t-db-1", "t-web-1")
	f.OnSequence("ls --format json --all", "[]", "["+running("t-db-1", "db", "t", nil)+"]")
	_, err := r.Up(context.Background(), UpOptions{Detach: true})
	if err == nil || !strings.Contains(err.Error(), "has no healthcheck") {
		t.Fatalf("expected healthcheck error, got %v", err)
	}
}

func TestUpAttachesForCompletedSuccessfully(t *testing.T) {
	r, f, errBuf := upFixture(t, `
name: t
services:
  app:
    image: docker.io/library/alpine:3.20
    depends_on:
      init:
        condition: service_completed_successfully
  init:
    image: docker.io/library/alpine:3.20
`, "t-init-1", "t-app-1")
	f.OnSequence("ls --format json --all", "[]", "["+running("t-init-1", "init", "t", nil)+"]")
	if _, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil {
		t.Fatalf("up: %v\n%s", err, errBuf)
	}
	if !f.Called("start --attach t-init-1") || f.Called("start t-init-1") {
		t.Fatalf("init must be started attached so its exit code is known: %v", f.Calls())
	}
	if !strings.Contains(errBuf.String(), "t-init-1  Exited (0)") {
		t.Fatalf("expected Exited (0) step:\n%s", errBuf)
	}
	if code, ok := r.recordedExit("t-init-1"); !ok || code != 0 {
		t.Fatalf("exit code must be recorded: %d %v", code, ok)
	}
}

func TestUpFailsWhenInitExitsNonZero(t *testing.T) {
	r, f, _ := upFixture(t, `
name: t
services:
  app:
    image: docker.io/library/alpine:3.20
    depends_on:
      init:
        condition: service_completed_successfully
  init:
    image: docker.io/library/alpine:3.20
`, "t-init-1", "t-app-1")
	f.OnSequence("ls --format json --all", "[]", "["+running("t-init-1", "init", "t", nil)+"]")
	f.On("start --attach t-init-1", "boom", 2)
	_, err := r.Up(context.Background(), UpOptions{Detach: true})
	if err == nil || !strings.Contains(err.Error(), "exited (2)") {
		t.Fatalf("expected exit 2 failure, got %v", err)
	}
	if f.Called("create --name t-app-1") {
		t.Fatal("app must not be created after init fails")
	}
}

func TestUpAttachedReturnsExitCodeFrom(t *testing.T) {
	r, f, errBuf := upFixture(t, `
name: t
services:
  web:
    image: docker.io/library/alpine:3.20
  test:
    image: docker.io/library/alpine:3.20
`, "t-web-1", "t-test-1")
	f.On("start --attach t-test-1", "assertion failed", 3)
	f.On("start --attach t-web-1", "serving", 0)
	code, err := r.Up(context.Background(), UpOptions{ExitCodeFrom: "test"})
	if err != nil {
		t.Fatalf("up: %v\n%s", err, errBuf)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3\n%s", code, errBuf)
	}
	out := r.Console.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "assertion failed") {
		t.Fatalf("attached output must be shown:\n%s", out)
	}
	if !strings.Contains(errBuf.String(), "t-test-1 exited with code 3") {
		t.Fatalf("expected exit notice:\n%s", errBuf)
	}
}

func TestUpAttachedFollowsExistingContainer(t *testing.T) {
	r, f, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	f.On("ls --format json --all", "["+running("t-a-1", "a", "t", map[string]string{project.LabelConfigHash: hashOf(t, r, "a")})+"]", 0)
	f.On("logs --follow t-a-1", "old line\n", 0)
	code, err := r.Up(context.Background(), UpOptions{})
	if err != nil || code != 0 {
		t.Fatalf("up: code=%d err=%v", code, err)
	}
	if !f.Called("logs --follow t-a-1") {
		t.Fatalf("an already running container must have its logs followed: %v", f.Calls())
	}
	if out := r.Console.Out.(*bytes.Buffer).String(); !strings.Contains(out, "old line") {
		t.Fatalf("followed logs must be printed:\n%s", out)
	}
}

func TestAttachNamesHonourFilters(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  web:
    image: img
    depends_on: [db]
  db:
    image: img
  worker:
    image: img
`, nil)
	if got := strings.Join(r.attachNames(UpOptions{}), ","); got != "db,web,worker" {
		t.Fatalf("default attach order = %q", got)
	}
	if got := strings.Join(r.attachNames(UpOptions{NoAttach: []string{"db"}}), ","); got != "web,worker" {
		t.Fatalf("--no-attach = %q", got)
	}
	if got := strings.Join(r.attachNames(UpOptions{Attach: []string{"worker"}}), ","); got != "worker" {
		t.Fatalf("--attach = %q", got)
	}
}

func TestStartHintExplainsVolumeAttachment(t *testing.T) {
	if startHint(nil) != nil {
		t.Fatal("nil must pass through")
	}
	err := startHint(context.DeadlineExceeded)
	if err != context.DeadlineExceeded {
		t.Fatal("unrelated errors must pass through")
	}
	hinted := startHint(errFrom("storage device attachment is invalid"))
	if !strings.Contains(hinted.Error(), "one container at a time") {
		t.Fatalf("expected volume hint, got %v", hinted)
	}
}

type errFrom string

func (e errFrom) Error() string { return string(e) }
