// Package engine drives Apple's `container` command line tool.
//
// The CLI is the documented, versioned public surface of the runtime, and its
// JSON output is stable within a major version, so apple-compose shells out to
// it rather than speaking XPC. Every subprocess goes through Engine so that
// dry-run, debugging, and error reporting behave the same everywhere.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// ErrNotFound is returned when the runtime has no resource with the given id.
var ErrNotFound = errors.New("not found")

// EnvDefaultDNS names the environment variable holding comma-separated
// nameservers applied to every container and build that does not set its
// own. It is a workaround for hosts where the runtime's resolver on the
// network gateway does not answer.
const EnvDefaultDNS = "APPLE_COMPOSE_DNS"

// DefaultDNS returns the nameservers from EnvDefaultDNS, if any.
func DefaultDNS() []string {
	var out []string
	for _, s := range strings.Split(os.Getenv(EnvDefaultDNS), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Engine locates and runs the `container` binary.
type Engine struct {
	// Bin is the absolute path of the `container` executable.
	Bin string
	// DryRun echoes commands that would mutate state instead of running them.
	DryRun bool
	// Debug echoes every command before it runs.
	Debug bool
	// Log receives dry-run and debug output. Defaults to os.Stderr.
	Log io.Writer
}

// ExitError describes a `container` invocation that returned non-zero.
type ExitError struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	msg = strings.TrimPrefix(msg, "Error: ")
	if msg == "" {
		return fmt.Sprintf("`container %s` exited with status %d", e.Verb(), e.Code)
	}
	return msg
}

// Verb returns the subcommand words of the failed invocation, for context.
func (e *ExitError) Verb() string {
	var words []string
	for _, a := range e.Args {
		if strings.HasPrefix(a, "-") {
			break
		}
		words = append(words, a)
		if len(words) == 2 {
			break
		}
	}
	return strings.Join(words, " ")
}

// Find resolves the `container` binary. CONTAINER_BIN wins, then PATH, then
// the standard install location.
func Find() (string, error) {
	if p := os.Getenv("CONTAINER_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("CONTAINER_BIN=%s: %w", p, err)
		}
		return p, nil
	}
	if p, err := exec.LookPath("container"); err == nil {
		return p, nil
	}
	candidates := []string{"/usr/local/bin/container"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "apple-container", "bin", "container"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errors.New("the `container` command was not found. Install Apple's container tool from https://github.com/apple/container/releases, or set CONTAINER_BIN")
}

// New builds an Engine for the located binary.
func New() (*Engine, error) {
	bin, err := Find()
	if err != nil {
		return nil, err
	}
	return &Engine{Bin: bin, Log: os.Stderr}, nil
}

func (e *Engine) log() io.Writer {
	if e.Log == nil {
		return os.Stderr
	}
	return e.Log
}

func (e *Engine) echo(prefix string, args []string) {
	fmt.Fprintf(e.log(), "%s container %s\n", prefix, shellJoin(args))
}

// Command returns an exec.Cmd for `container args...` with no stdio wired.
func (e *Engine) Command(ctx context.Context, args ...string) *exec.Cmd {
	if e.Debug {
		e.echo("[debug]", args)
	}
	cmd := exec.CommandContext(ctx, e.Bin, args...)
	cmd.Env = append(os.Environ(), "CONTAINER_PROGRESS=none")
	return cmd
}

// Output runs `container args...` and returns stdout. Progress output from the
// CLI is suppressed. A non-zero exit becomes an *ExitError carrying stderr.
func (e *Engine) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := e.Command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return stdout.Bytes(), &ExitError{Args: args, Code: xe.ExitCode(), Stderr: stderr.String()}
		}
		return nil, fmt.Errorf("container %s: %w", strings.Join(args, " "), err)
	}
	return stdout.Bytes(), nil
}

// Mutate is Output for state-changing commands: honoured by DryRun.
func (e *Engine) Mutate(ctx context.Context, args ...string) ([]byte, error) {
	if e.DryRun {
		e.echo("[dry-run]", args)
		return nil, nil
	}
	return e.Output(ctx, args...)
}

// Run runs `container args...` with the given stdio attached and returns the
// process exit code. Errors that are not exit statuses are returned as err.
func (e *Engine) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) (int, error) {
	if e.DryRun {
		e.echo("[dry-run]", args)
		return 0, nil
	}
	cmd := e.Command(ctx, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Long pulls and builds show their progress when a person is watching.
	if f, ok := stderr.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		cmd.Env = append(cmd.Env, "CONTAINER_PROGRESS=auto")
	}
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return xe.ExitCode(), nil
	}
	return -1, fmt.Errorf("container %s: %w", strings.Join(args, " "), err)
}

// JSON runs a query command with `--format json` and decodes stdout into v.
func (e *Engine) JSON(ctx context.Context, v any, args ...string) error {
	out, err := e.Output(ctx, args...)
	if err != nil {
		return err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("container %s: cannot decode JSON output: %w", strings.Join(args, " "), err)
	}
	return nil
}

// IsNotFound reports whether err is ErrNotFound or a runtime failure that
// says the resource does not exist.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || isNotFound(err)
}

func isNotFound(err error) bool {
	var xe *ExitError
	if !errors.As(err, &xe) {
		return false
	}
	s := strings.ToLower(xe.Stderr)
	return strings.Contains(s, "not found") || strings.Contains(s, "notfound") || strings.Contains(s, "does not exist") || strings.Contains(s, "no such")
}

// shellJoin quotes arguments for display only.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n'\"$&|;<>()*?[]{}\\") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
