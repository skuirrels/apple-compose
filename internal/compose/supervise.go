package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/state"
)

// LabelRestart records a service's restart policy on its container so the
// supervisor can honour it without reading the compose file.
const LabelRestart = "com.apple-compose.restart"

// SuperviseOptions configure Supervise.
type SuperviseOptions struct {
	// Interval is the polling period; the runtime has no event stream.
	Interval time.Duration
	// Delay is the pause before restarting an exited container.
	Delay time.Duration
	// Once makes a single pass and returns, for tests.
	Once bool
}

// supervised is the policy state the supervisor keeps per container.
type supervised struct {
	restarts int
	// settled containers have exhausted their policy; they are left alone
	// until they are recreated.
	settled bool
}

// Supervise restarts exited containers of the project according to their
// restart policies, as Docker's daemon would. It returns when nothing is left
// to supervise: every policy container is either gone, stopped on purpose, or
// has exhausted its retries. Containers stopped by an apple-compose command
// carry a marker in the state directory and are never restarted.
func (r *Runner) Supervise(ctx context.Context, o SuperviseOptions) error {
	if o.Interval <= 0 {
		o.Interval = 2 * time.Second
	}
	if o.Delay <= 0 {
		o.Delay = time.Second
	}
	states := map[string]*supervised{}
	var (
		mu       sync.Mutex
		attached = map[string]bool{}
	)
	isAttached := func(id string) bool {
		mu.Lock()
		defer mu.Unlock()
		return attached[id]
	}
	for {
		cs, err := r.containers(ctx, true)
		if err != nil {
			return err
		}
		active := 0
		for i := range cs {
			c := cs[i]
			policy := c.Label(LabelRestart)
			if policy == "" || policy == "no" || c.Label(project.LabelOneOff) == "True" {
				continue
			}
			key := c.ID + "@" + c.Configuration.CreationDate.UTC().Format(time.RFC3339Nano)
			st := states[key]
			if st == nil {
				st = &supervised{}
				states[key] = st
			}
			if st.settled {
				continue
			}
			if c.Running() || isAttached(c.ID) {
				active++
				continue
			}
			if stoppedOnPurpose(r.Project.Name, c.ID) {
				continue
			}
			code, known := r.recordedExit(c.ID)
			if !known {
				// The runtime does not report exit codes for containers
				// started detached, so the first exit is assumed to be a
				// failure. Later exits are attached and therefore exact.
				code = 1
			}
			if !shouldRestart(policy, code) || retriesExhausted(policy, st.restarts) {
				r.Console.Info("%s exited with code %d, not restarting (%s)", c.ID, code, policy)
				st.settled = true
				continue
			}
			st.restarts++
			active++
			recordRestarts(r.Project.Name, c.ID, st.restarts)
			r.Console.Info("%s exited with code %d, restarting (%s, attempt %d)", c.ID, code, policy, st.restarts)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(o.Delay):
			}
			mu.Lock()
			attached[c.ID] = true
			mu.Unlock()
			id := c.ID
			go func() {
				// Attaching is the only way to learn the exit code.
				exit, err := r.Engine.AttachBackground(ctx, id, discard, discard)
				if err != nil {
					exit = 1
				}
				r.recordExit(id, exit)
				mu.Lock()
				delete(attached, id)
				mu.Unlock()
			}()
			if _, err := r.waitRunning(ctx, id, 2*time.Minute); err == nil {
				_ = r.RefreshHosts(ctx)
			}
		}
		if o.Once || active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.Interval):
		}
	}
}

// restartsPath is the file holding how often the supervisor restarted a
// container, shown by `ps`.
func restartsPath(projectName, id string) string {
	dir, err := state.ProjectDir(projectName)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "restarts", id)
}

func recordRestarts(projectName, id string, n int) {
	p := restartsPath(projectName, id)
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(strconv.Itoa(n)), 0o644)
}

// restartCount returns how often the supervisor restarted a container.
func restartCount(projectName, id string) int {
	b, err := os.ReadFile(restartsPath(projectName, id))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func clearRestarts(projectName string, ids []string) {
	for _, id := range ids {
		if p := restartsPath(projectName, id); p != "" {
			_ = os.Remove(p)
		}
	}
}

// retriesExhausted applies the optional on-failure:N limit.
func retriesExhausted(policy string, restarts int) bool {
	if _, n, ok := strings.Cut(policy, ":"); ok {
		if max, err := strconv.Atoi(n); err == nil && max > 0 {
			return restarts >= max
		}
	}
	return false
}

// needsSupervisor reports whether a running container of the project carries
// a restart policy. Labels are consulted rather than the compose file so
// file-less commands such as `start -p name` behave the same.
func (r *Runner) needsSupervisor(ctx context.Context) (bool, error) {
	cs, err := r.containers(ctx, false)
	if err != nil {
		return false, err
	}
	for _, c := range cs {
		if v := c.Label(LabelRestart); v != "" && v != "no" && c.Label(project.LabelOneOff) != "True" {
			return true, nil
		}
	}
	return false, nil
}

// stopMarker is the file recording that apple-compose stopped a container on
// purpose, so the supervisor leaves it alone.
func stopMarker(projectName, id string) string {
	dir, err := state.ProjectDir(projectName)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "stopped", id)
}

func markStopped(projectName string, ids []string) {
	for _, id := range ids {
		p := stopMarker(projectName, id)
		if p == "" {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, nil, 0o644)
	}
}

func clearStopped(projectName string, ids []string) {
	for _, id := range ids {
		if p := stopMarker(projectName, id); p != "" {
			_ = os.Remove(p)
		}
	}
}

func stoppedOnPurpose(projectName, id string) bool {
	p := stopMarker(projectName, id)
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// SupervisorSpawner launches the background supervisor for a project and
// returns its process id. The default runs this executable's hidden
// `supervise` command as a daemon; tests substitute their own.
type SupervisorSpawner func(projectName string, configFiles []string, workingDir, logPath string) (int, error)

// spawnSupervisor is the default SupervisorSpawner.
func spawnSupervisor(projectName string, configFiles []string, workingDir, logPath string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	args := []string{"--project-name", projectName}
	for _, f := range configFiles {
		args = append(args, "--file", f)
	}
	if workingDir != "" {
		args = append(args, "--project-directory", workingDir)
	}
	args = append(args, "supervise")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "CONTAINER_PROGRESS=none")
	// A new session detaches the supervisor from the terminal, so closing
	// the window does not take it down with the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// supervisorPaths returns the lock, pid, and log files of a project's
// supervisor.
func supervisorPaths(projectName string) (lock, pid, log string, err error) {
	dir, err := state.ProjectDir(projectName)
	if err != nil {
		return "", "", "", err
	}
	return filepath.Join(dir, "supervisor.lock"), filepath.Join(dir, "supervisor.pid"), filepath.Join(dir, "supervisor.log"), nil
}

// HoldSupervisorLock takes the project's supervisor lock for the life of the
// process. It fails when another supervisor already holds it.
func HoldSupervisorLock(projectName string) (io.Closer, error) {
	lock, pidPath, _, err := supervisorPaths(projectName)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("a supervisor for project %s is already running", projectName)
	}
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)
	return f, nil
}

// supervisorRunning reports whether a supervisor holds the project lock.
func supervisorRunning(projectName string) (bool, error) {
	lock, _, _, err := supervisorPaths(projectName)
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, err
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}

// EnsureSupervisor starts the background supervisor when the project has
// restart policies and no supervisor is running yet.
func (r *Runner) EnsureSupervisor(ctx context.Context) error {
	if r.Engine.DryRun {
		return nil
	}
	if need, err := r.needsSupervisor(ctx); err != nil || !need {
		return err
	}
	running, err := supervisorRunning(r.Project.Name)
	if err != nil || running {
		return err
	}
	_, _, logPath, err := supervisorPaths(r.Project.Name)
	if err != nil {
		return err
	}
	spawn := r.SpawnSupervisor
	if spawn == nil {
		spawn = spawnSupervisor
	}
	pid, err := spawn(r.Project.Name, r.Project.ComposeFiles, r.Project.WorkingDir, logPath)
	if err != nil {
		r.Console.Warn("restart policies are not supervised: %v", err)
		return nil
	}
	r.Console.Step("Supervisor", fmt.Sprintf("%s (pid %d)", r.Project.Name, pid), "Started")
	return nil
}

// StopSupervisor terminates the project's background supervisor, if any.
func (r *Runner) StopSupervisor() {
	running, err := supervisorRunning(r.Project.Name)
	if err != nil || !running {
		return
	}
	_, pidPath, _, err := supervisorPaths(r.Project.Name)
	if err != nil {
		return
	}
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err == nil {
		r.Console.Step("Supervisor", r.Project.Name, "Stopped")
	}
}

// restartLabel returns the value stored in LabelRestart for a service.
func restartLabel(policy string) string {
	if policy == "" || policy == "no" {
		return ""
	}
	return policy
}
