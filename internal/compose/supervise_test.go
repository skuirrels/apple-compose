package compose

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const restartService = `
name: t
services:
  a:
    image: docker.io/library/alpine:3.20
    restart: on-failure:2
`

func TestCreateArgsCarryRestartLabel(t *testing.T) {
	r, _ := loadRunner(t, restartService, nil)
	if got := argsFor(t, r, "a"); !strings.Contains(got, "--label "+LabelRestart+"=on-failure:2") {
		t.Fatalf("restart label missing: %s", got)
	}
	s, _ := r.Project.GetService("a")
	args, err := r.createArgs(createSpec{service: s, name: "t-a-run-x", number: 1, oneOff: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), LabelRestart) {
		t.Fatal("one-off containers must not be supervised")
	}
}

func TestSuperviseRestartsExitedContainer(t *testing.T) {
	r, f, errBuf := upFixture(t, restartService, "t-a-1")
	f.On("ls --format json --all", "["+stopped("t-a-1", "a", "t", map[string]string{LabelRestart: "on-failure:2"})+"]", 0)
	if err := r.Supervise(context.Background(), SuperviseOptions{Once: true, Delay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("start --attach t-a-1") {
		t.Fatalf("an exited container with a policy must be restarted attached: %v", f.Calls())
	}
	if !strings.Contains(errBuf.String(), "t-a-1 exited with code 1, restarting (on-failure:2, attempt 1)") {
		t.Fatalf("expected restart notice:\n%s", errBuf)
	}
}

func TestSuperviseHonoursExitCodeAndRetryLimit(t *testing.T) {
	r, f, errBuf := upFixture(t, restartService, "t-a-1")
	f.On("ls --format json --all", "["+stopped("t-a-1", "a", "t", map[string]string{LabelRestart: "on-failure:2"})+"]", 0)
	r.recordExit("t-a-1", 0)
	if err := r.Supervise(context.Background(), SuperviseOptions{Once: true}); err != nil {
		t.Fatal(err)
	}
	if f.Called("start") || !strings.Contains(errBuf.String(), "not restarting") {
		t.Fatalf("a clean exit must not restart under on-failure:\n%s\n%v", errBuf, f.Calls())
	}
	// Two failures use up the retry budget; the third is left alone.
	r.recordExit("t-a-1", 3)
	f.On("start --attach t-a-1", "", 3)
	f.On("inspect t-a-1", "["+stopped("t-a-1", "a", "t", map[string]string{LabelRestart: "on-failure:2"})+"]", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.Supervise(ctx, SuperviseOptions{Interval: 10 * time.Millisecond, Delay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(f.Calls(), "\n"), "start --attach t-a-1"); n != 2 {
		t.Fatalf("on-failure:2 must restart exactly twice, got %d:\n%s", n, errBuf)
	}
	if !strings.Contains(errBuf.String(), "attempt 2") || !strings.Contains(errBuf.String(), "not restarting (on-failure:2)") {
		t.Fatalf("expected retry exhaustion:\n%s", errBuf)
	}
}

func TestSuperviseSkipsContainersStoppedOnPurpose(t *testing.T) {
	r, f, _ := upFixture(t, restartService, "t-a-1")
	f.On("ls --format json --all", "["+stopped("t-a-1", "a", "t", map[string]string{LabelRestart: "always"})+"]", 0)
	markStopped("t", []string{"t-a-1"})
	if err := r.Supervise(context.Background(), SuperviseOptions{Once: true}); err != nil {
		t.Fatal(err)
	}
	if f.Called("start") {
		t.Fatalf("a container stopped by apple-compose must stay stopped: %v", f.Calls())
	}
	clearStopped("t", []string{"t-a-1"})
	if err := r.Supervise(context.Background(), SuperviseOptions{Once: true, Delay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if !f.Called("start --attach t-a-1") {
		t.Fatalf("clearing the marker must allow restarts again: %v", f.Calls())
	}
}

func TestStopAndKillMarkContainers(t *testing.T) {
	r, f, _ := upFixture(t, twoServices)
	f.On("ls --format json", "["+running("t-db-1", "db", "t", nil)+","+running("t-web-1", "web", "t", nil)+"]", 0)
	if err := r.Stop(context.Background(), []string{"web"}, 0); err != nil {
		t.Fatal(err)
	}
	if !stoppedOnPurpose("t", "t-web-1") || stoppedOnPurpose("t", "t-db-1") {
		t.Fatal("stop must mark exactly the stopped containers")
	}
	if err := r.Kill(context.Background(), []string{"db"}, ""); err != nil {
		t.Fatal(err)
	}
	if !stoppedOnPurpose("t", "t-db-1") {
		t.Fatal("kill must mark the killed container")
	}
}

func TestUpClearsMarkersAndStartsSupervisor(t *testing.T) {
	r, f, _ := upFixture(t, restartService, "t-a-1")
	markStopped("t", []string{"t-a-1"})
	f.OnSequence("ls --format json --all", "[]", "["+running("t-a-1", "a", "t", map[string]string{LabelRestart: "on-failure:2"})+"]")
	f.On("ls --format json", "["+running("t-a-1", "a", "t", map[string]string{LabelRestart: "on-failure:2"})+"]", 0)
	var spawned []string
	r.SpawnSupervisor = func(name string, files []string, dir, log string) (int, error) {
		spawned = append(spawned, name+" "+strings.Join(files, ",")+" "+dir+" "+log)
		return 4242, nil
	}
	if _, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil {
		t.Fatal(err)
	}
	if stoppedOnPurpose("t", "t-a-1") {
		t.Fatal("up must clear the stop marker before starting")
	}
	if len(spawned) != 1 || !strings.HasPrefix(spawned[0], "t ") || !strings.Contains(spawned[0], "compose.yaml") || !strings.HasSuffix(spawned[0], "supervisor.log") {
		t.Fatalf("supervisor must be spawned with the project's files: %v", spawned)
	}
}

func TestUpWithoutPoliciesSpawnsNothing(t *testing.T) {
	r, f, _ := upFixture(t, "name: t\nservices:\n  a:\n    image: docker.io/library/alpine:3.20\n", "t-a-1")
	f.On("ls --format json", "["+running("t-a-1", "a", "t", nil)+"]", 0)
	r.SpawnSupervisor = func(string, []string, string, string) (int, error) {
		t.Fatal("no service has a restart policy")
		return 0, nil
	}
	if _, err := r.Up(context.Background(), UpOptions{Detach: true}); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorLockPreventsSecondSpawn(t *testing.T) {
	r, f, errBuf := upFixture(t, restartService)
	f.On("ls --format json", "["+running("t-a-1", "a", "t", map[string]string{LabelRestart: "always"})+"]", 0)
	lock, err := HoldSupervisorLock("t")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := HoldSupervisorLock("t"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock must fail, got %v", err)
	}
	r.SpawnSupervisor = func(string, []string, string, string) (int, error) {
		t.Fatal("a supervisor is already running")
		return 0, nil
	}
	if err := r.EnsureSupervisor(context.Background()); err != nil {
		t.Fatal(err)
	}
	// StopSupervisor signals the pid on record; point it at a throwaway
	// process rather than this test binary.
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleep.Process.Kill(); _ = sleep.Wait() }()
	_, pidPath, _, _ := supervisorPaths("t")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(sleep.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	r.StopSupervisor()
	if !strings.Contains(errBuf.String(), "Supervisor t  Stopped") {
		t.Fatalf("expected stop step:\n%s", errBuf)
	}
	done := make(chan error, 1)
	go func() { done <- sleep.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor process must be terminated")
	}
}

func TestDownStopsSupervisorFirst(t *testing.T) {
	r, f, _ := upFixture(t, restartService)
	f.On("ls --format json --all", "[]", 0)
	f.On("network ls --format json", "[]", 0)
	stopped := false
	lock, err := HoldSupervisorLock("t")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleep.Process.Kill(); _ = sleep.Wait() }()
	_, pidPath, _, _ := supervisorPaths("t")
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(sleep.Process.Pid)), 0o644)
	go func() { _ = sleep.Wait(); stopped = true }()
	if err := r.Down(context.Background(), DownOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if !stopped {
		t.Fatal("down must terminate the supervisor")
	}
}

func TestRetriesExhausted(t *testing.T) {
	if retriesExhausted("always", 100) || retriesExhausted("on-failure", 100) {
		t.Fatal("unbounded policies never exhaust")
	}
	if retriesExhausted("on-failure:3", 2) || !retriesExhausted("on-failure:3", 3) {
		t.Fatal("on-failure:3 allows three restarts")
	}
	if restartLabel("no") != "" || restartLabel("") != "" || restartLabel("unless-stopped") != "unless-stopped" {
		t.Fatal("restartLabel must drop the no policy")
	}
}

func TestSupervisorRestartCountShowsInPs(t *testing.T) {
	r, f, _ := upFixture(t, restartService, "t-a-1")
	f.On("ls --format json --all", "["+stopped("t-a-1", "a", "t", map[string]string{LabelRestart: "always"})+"]", 0)
	if err := r.Supervise(context.Background(), SuperviseOptions{Once: true, Delay: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if n := restartCount("t", "t-a-1"); n != 1 {
		t.Fatalf("restart count = %d, want 1", n)
	}
	f.On("ls --format json", "["+running("t-a-1", "a", "t", map[string]string{LabelRestart: "always"})+"]", 0)
	rows, err := r.Rows(context.Background(), PsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Restarts != 1 || !strings.HasSuffix(rows[0].Status, "(restarted 1)") {
		t.Fatalf("ps must show the restart count: %+v", rows)
	}
	// Recreating the container starts the count afresh.
	s, _ := r.Project.GetService("a")
	if err := r.createContainer(context.Background(), s, 1, "t-a-1", "h"); err != nil {
		t.Fatal(err)
	}
	if n := restartCount("t", "t-a-1"); n != 0 {
		t.Fatalf("recreate must reset the count, got %d", n)
	}
}
