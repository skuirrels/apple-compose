package compose

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skuirrels/apple-compose/internal/project"
)

func TestExecTargetsRunningReplica(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json", "["+
		running("t-web-1", "web", "t", nil)+","+
		running("t-web-2", "web", "t", map[string]string{project.LabelContainerNumber: "2"})+
		"]", 0)
	code, err := r.Exec(context.Background(), ExecOptions{Service: "web", Index: 2, NoTTY: true, User: "app", WorkDir: "/srv", Env: []string{"A=1"}, Command: []string{"ls", "-l"}})
	if err != nil || code != 0 {
		t.Fatalf("exec = %d, %v", code, err)
	}
	if got := f.Call("exec"); got != "exec --user app --workdir /srv --env A=1 t-web-2 ls -l" {
		t.Fatalf("exec = %q", got)
	}
	f.Reset()
	f.On("exec", "", 7)
	code, err = r.Exec(context.Background(), ExecOptions{Service: "web", Detach: true, Interactive: true, Command: []string{"true"}})
	if err != nil || code != 7 {
		t.Fatalf("exec must return the command's exit code: %d, %v", code, err)
	}
	if got := f.Call("exec"); got != "exec --detach t-web-1 true" {
		t.Fatalf("detached exec must drop interactive flags: %q", got)
	}
	if _, err := r.Exec(context.Background(), ExecOptions{Service: "db", Command: []string{"true"}}); err == nil || !strings.Contains(err.Error(), "no running container") {
		t.Fatalf("expected no running container error, got %v", err)
	}
}

func TestRunCreatesOneOffAndRemovesIt(t *testing.T) {
	r, f, _ := upFixture(t, twoServices, "t-db-1", "t-web-run-x")
	// Dependencies come up first; afterwards the listing shows them.
	f.OnSequence("ls --format json --all", "[]", "["+running("t-db-1", "db", "t", nil)+"]")
	f.On("start --attach t-web-run-x", "", 5)
	entry := "sh"
	code, err := r.Run(context.Background(), RunOptions{
		Service: "web", Name: "t-web-run-x", Remove: true, NoTTY: true,
		Command: []string{"-c", "exit 5"}, Entrypoint: &entry, Env: []string{"X=1"},
		Labels: map[string]string{"custom": "yes"}, Publish: []string{"9000:80"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 5 {
		t.Fatalf("run must return the one-off exit code, got %d", code)
	}
	calls := f.Calls()
	if !f.Called("create --name t-db-1") || !f.Called("start t-db-1") {
		t.Fatalf("dependencies must be brought up:\n%s", strings.Join(calls, "\n"))
	}
	create := f.Call("create --name t-web-run-x")
	for _, want := range []string{
		"--label " + project.LabelOneOff + "=True",
		"--label custom=yes",
		"--env X=1",
		"--publish 9000:80",
		"--entrypoint sh",
		" -c exit 5",
	} {
		if !strings.Contains(create, want) {
			t.Fatalf("one-off create lacks %q:\n%s", want, create)
		}
	}
	if !f.Called("delete --force t-web-run-x") {
		t.Fatalf("--rm must delete the one-off:\n%s", strings.Join(calls, "\n"))
	}
	if got, ok := r.recordedExit("t-web-run-x"); !ok || got != 5 {
		t.Fatalf("one-off exit must be recorded: %d %v", got, ok)
	}
}

func TestRunDetachedPrintsNameAndSkipsDeps(t *testing.T) {
	r, f, _ := upFixture(t, twoServices, "t-web-run-y")
	code, err := r.Run(context.Background(), RunOptions{Service: "web", Name: "t-web-run-y", Detach: true, NoDeps: true, Remove: true})
	if err != nil || code != 0 {
		t.Fatalf("run = %d, %v", code, err)
	}
	if f.Called("create --name t-db-1") {
		t.Fatalf("--no-deps must skip dependencies: %v", f.Calls())
	}
	if !f.Called("start t-web-run-y") || f.Called("start --attach") {
		t.Fatalf("detached run must start without attaching: %v", f.Calls())
	}
	if !strings.Contains(f.Call("create --name t-web-run-y"), "--rm") {
		t.Fatalf("detached --rm must be delegated to the runtime: %q", f.Call("create"))
	}
	if got := strings.TrimSpace(r.Console.Out.(*bytes.Buffer).String()); got != "t-web-run-y" {
		t.Fatalf("detached run must print the container name, got %q", got)
	}
}

func TestRunUnknownService(t *testing.T) {
	r, _, _ := upFixture(t, twoServices)
	if _, err := r.Run(context.Background(), RunOptions{Service: "nope"}); err == nil {
		t.Fatal("unknown service must fail")
	}
	if len(slug()) != 12 {
		t.Fatal("slug must be twelve hex characters")
	}
}

func TestLogsPrefixesAndFilters(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+
		running("t-db-1", "db", "t", nil)+","+
		running("t-web-1", "web", "t", nil)+","+
		running("t-web-2", "web", "t", map[string]string{project.LabelContainerNumber: "2"})+
		"]", 0)
	f.On("logs -n 20 t-db-1", "db says hi\n", 0)
	f.On("logs -n 20 t-web-1", "web one\n", 0)
	f.On("logs -n 20 t-web-2", "web two\n", 0)
	out := r.Console.Out.(*bytes.Buffer)
	if err := r.Logs(context.Background(), LogsOptions{Tail: 20}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"t-db-1", "db says hi", "web one", "web two"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("logs output lacks %q:\n%s", want, out)
		}
	}
	out.Reset()
	f.Reset()
	if err := r.Logs(context.Background(), LogsOptions{Tail: 20, Services: []string{"web"}, Index: 2, NoLogPrefix: true}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "web two\n" {
		t.Fatalf("filtered logs = %q", got)
	}
	if f.Called("logs -n 20 t-db-1") {
		t.Fatal("unselected services must not be read")
	}
}

func TestLogsSinceSkipsOldStoredOutput(t *testing.T) {
	r, f, errBuf := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+running("t-db-1", "db", "t", nil)+"]", 0)
	f.On("logs", "stale\n", 0)
	out := r.Console.Out.(*bytes.Buffer)
	// The fixture container started at 2026-09-09; a window after that
	// excludes its stored output entirely when not following.
	if err := r.Logs(context.Background(), LogsOptions{Tail: -1, Since: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 || f.Called("logs") {
		t.Fatalf("stored output outside the window must be skipped: %q %v", out.String(), f.Calls())
	}
	if !strings.Contains(errBuf.String(), "stores log lines without timestamps") {
		t.Fatalf("expected window warning:\n%s", errBuf)
	}
	out.Reset()
	if err := r.Logs(context.Background(), LogsOptions{Tail: -1, Since: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Timestamps: true}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "stale") || !strings.Contains(got, "T") {
		t.Fatalf("output inside the window must be stamped and shown: %q", got)
	}
}

func TestLogsIgnoresNeverStartedContainers(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json --all", "["+`{"id":"t-db-1","configuration":{"labels":{"com.docker.compose.project":"t","com.docker.compose.service":"db"}},"status":{"state":"stopped"}}`+"]", 0)
	if err := r.Logs(context.Background(), LogsOptions{Tail: -1}); err != nil {
		t.Fatal(err)
	}
	if f.Called("logs") {
		t.Fatalf("a container that never ran has no logs to read: %v", f.Calls())
	}
}

func TestDownRemovesImages(t *testing.T) {
	r, errBuf := loadRunner(t, `
name: t
services:
  built:
    build: .
  pulled:
    image: docker.io/library/alpine:3.20
`, map[string]string{"Dockerfile": "FROM alpine"})
	f := withFake(t, r)
	f.On("ls --format json --all", "["+stopped("t-built-1", "built", "t", nil)+","+stopped("t-pulled-1", "pulled", "t", nil)+"]", 0)
	f.On("network ls --format json", "[]", 0)
	if err := r.Down(context.Background(), DownOptions{RemoveImages: "local"}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("image delete t-built") || f.Called("image delete docker.io") {
		t.Fatalf("local mode must remove only built images: %v", f.Calls())
	}
	f.Reset()
	f.On("image delete docker.io/library/alpine:3.20", "Error: not found", 1)
	if err := r.Down(context.Background(), DownOptions{RemoveImages: "all"}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("image delete t-built") || !f.Called("image delete docker.io/library/alpine:3.20") {
		t.Fatalf("all mode must remove every image: %v", f.Calls())
	}
	if strings.Contains(errBuf.String(), "Image docker.io/library/alpine:3.20  Removing") {
		t.Fatalf("a missing image is not a failure:\n%s", errBuf)
	}
}

func TestUpWaitBlocksOnHealth(t *testing.T) {
	r, f, errBuf := upFixture(t, `
name: t
services:
  a:
    image: docker.io/library/alpine:3.20
    healthcheck:
      test: ["CMD", "true"]
`, "t-a-1")
	f.OnSequence("ls --format json --all", "[]", "["+running("t-a-1", "a", "t", nil)+"]")
	if _, err := r.Up(context.Background(), UpOptions{Detach: true, Wait: true}); err != nil {
		t.Fatalf("up --wait: %v\n%s", err, errBuf)
	}
	if !f.Called("exec t-a-1 true") || strings.Count(errBuf.String(), "Healthy") != 1 {
		t.Fatalf("--wait must probe once and report Healthy once:\n%s\n%v", errBuf, f.Calls())
	}
}
