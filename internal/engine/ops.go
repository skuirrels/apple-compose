package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LabelProject is the label carrying the compose project name.
const LabelProject = "com.docker.compose.project"

// CheckRunning verifies that the runtime's system services answer.
func (e *Engine) CheckRunning(ctx context.Context) error {
	if _, err := e.Output(ctx, "system", "status"); err != nil {
		return fmt.Errorf("the container runtime is not running (start it with `container system start`): %w", err)
	}
	return nil
}

// Version returns the CLI version string, e.g. "1.3.1".
func (e *Engine) Version(ctx context.Context) (string, error) {
	out, err := e.Output(ctx, "--version")
	if err != nil {
		return "", err
	}
	// "container CLI version 1.3.1 (build: release, commit: a9a62e2)"
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return strings.TrimSpace(string(out)), nil
}

// ListContainers lists containers; all includes stopped ones.
func (e *Engine) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	args := []string{"ls", "--format", "json"}
	if all {
		args = append(args, "--all")
	}
	var out []Container
	if err := e.JSON(ctx, &out, args...); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ProjectContainers lists containers labelled with the project name.
func (e *Engine) ProjectContainers(ctx context.Context, project string, all bool) ([]Container, error) {
	cs, err := e.ListContainers(ctx, all)
	if err != nil {
		return nil, err
	}
	var out []Container
	for _, c := range cs {
		if c.Label(LabelProject) == project {
			out = append(out, c)
		}
	}
	return out, nil
}

// InspectContainer returns a container by id, or ErrNotFound.
func (e *Engine) InspectContainer(ctx context.Context, id string) (*Container, error) {
	var out []Container
	if err := e.JSON(ctx, &out, "inspect", id); err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// Create runs `container create args...` and returns the new container id.
func (e *Engine) Create(ctx context.Context, args ...string) (string, error) {
	out, err := e.Mutate(ctx, append([]string{"create"}, args...)...)
	if err != nil {
		return "", err
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return "", nil
	}
	return lines[len(lines)-1], nil
}

// Start starts a created or stopped container detached.
func (e *Engine) Start(ctx context.Context, id string) error {
	_, err := e.Mutate(ctx, "start", id)
	return err
}

// Attach starts a stopped container with stdio attached and returns the exit
// code of its init process. The runtime cannot attach to a running container.
func (e *Engine) Attach(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer, interactive bool) (int, error) {
	args := []string{"start", "--attach"}
	if interactive {
		args = append(args, "--interactive")
	}
	args = append(args, id)
	return e.Run(ctx, stdin, stdout, stderr, args...)
}

// Stop stops containers with an optional signal and grace period.
func (e *Engine) Stop(ctx context.Context, ids []string, signal string, timeout time.Duration) error {
	if len(ids) == 0 {
		return nil
	}
	args := []string{"stop"}
	if signal != "" {
		args = append(args, "--signal", signal)
	}
	if timeout > 0 {
		args = append(args, "--time", strconv.Itoa(int(timeout.Round(time.Second)/time.Second)))
	}
	_, err := e.Mutate(ctx, append(args, ids...)...)
	return err
}

// Kill signals running containers.
func (e *Engine) Kill(ctx context.Context, ids []string, signal string) error {
	if len(ids) == 0 {
		return nil
	}
	args := []string{"kill"}
	if signal != "" {
		args = append(args, "--signal", signal)
	}
	_, err := e.Mutate(ctx, append(args, ids...)...)
	return err
}

// Delete removes containers; force removes running ones.
func (e *Engine) Delete(ctx context.Context, ids []string, force bool) error {
	if len(ids) == 0 {
		return nil
	}
	args := []string{"delete"}
	if force {
		args = append(args, "--force")
	}
	_, err := e.Mutate(ctx, append(args, ids...)...)
	return err
}

// LogsCommand returns a command streaming a container's stdio log. With
// follow, the command keeps running after the container exits, so callers
// must kill it themselves.
func (e *Engine) LogsCommand(ctx context.Context, id string, follow bool, tail int) *exec.Cmd {
	args := []string{"logs"}
	if follow {
		args = append(args, "--follow")
	}
	if tail >= 0 {
		args = append(args, "-n", strconv.Itoa(tail))
	}
	args = append(args, id)
	return e.Command(ctx, args...)
}

// ExecOptions configure Exec.
type ExecOptions struct {
	Interactive bool
	TTY         bool
	Detach      bool
	User        string
	WorkDir     string
	Env         []string
	EnvFiles    []string
}

// Exec runs a command in a running container and returns its exit code.
func (e *Engine) Exec(ctx context.Context, id string, o ExecOptions, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	args := []string{"exec"}
	if o.Interactive {
		args = append(args, "--interactive")
	}
	if o.TTY {
		args = append(args, "--tty")
	}
	if o.Detach {
		args = append(args, "--detach")
	}
	if o.User != "" {
		args = append(args, "--user", o.User)
	}
	if o.WorkDir != "" {
		args = append(args, "--workdir", o.WorkDir)
	}
	for _, kv := range o.Env {
		args = append(args, "--env", kv)
	}
	for _, f := range o.EnvFiles {
		args = append(args, "--env-file", f)
	}
	args = append(args, id)
	args = append(args, cmd...)
	return e.Run(ctx, stdin, stdout, stderr, args...)
}

// ListNetworks lists networks.
func (e *Engine) ListNetworks(ctx context.Context) ([]Network, error) {
	var out []Network
	err := e.JSON(ctx, &out, "network", "ls", "--format", "json")
	return out, err
}

// InspectNetwork returns a network or ErrNotFound.
func (e *Engine) InspectNetwork(ctx context.Context, name string) (*Network, error) {
	var out []Network
	if err := e.JSON(ctx, &out, "network", "inspect", name); err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// NetworkOptions are the creation options the runtime supports.
type NetworkOptions struct {
	Subnet   string
	SubnetV6 string
	Internal bool
	Labels   map[string]string
}

// CreateNetwork creates a vmnet network.
func (e *Engine) CreateNetwork(ctx context.Context, name string, o NetworkOptions) error {
	args := []string{"network", "create"}
	if o.Subnet != "" {
		args = append(args, "--subnet", o.Subnet)
	}
	if o.SubnetV6 != "" {
		args = append(args, "--subnet-v6", o.SubnetV6)
	}
	if o.Internal {
		args = append(args, "--internal")
	}
	for _, k := range sortedKeys(o.Labels) {
		args = append(args, "--label", k+"="+o.Labels[k])
	}
	_, err := e.Mutate(ctx, append(args, name)...)
	return err
}

// DeleteNetwork removes a network.
func (e *Engine) DeleteNetwork(ctx context.Context, name string) error {
	_, err := e.Mutate(ctx, "network", "delete", name)
	return err
}

// ListVolumes lists named volumes.
func (e *Engine) ListVolumes(ctx context.Context) ([]Volume, error) {
	var out []Volume
	err := e.JSON(ctx, &out, "volume", "ls", "--format", "json")
	return out, err
}

// InspectVolume returns a volume or ErrNotFound.
func (e *Engine) InspectVolume(ctx context.Context, name string) (*Volume, error) {
	var out []Volume
	if err := e.JSON(ctx, &out, "volume", "inspect", name); err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// CreateVolume creates a named volume. Driver options map to --opt.
func (e *Engine) CreateVolume(ctx context.Context, name string, labels, opts map[string]string) error {
	args := []string{"volume", "create"}
	for _, k := range sortedKeys(labels) {
		args = append(args, "--label", k+"="+labels[k])
	}
	for _, k := range sortedKeys(opts) {
		args = append(args, "--opt", k+"="+opts[k])
	}
	_, err := e.Mutate(ctx, append(args, name)...)
	return err
}

// DeleteVolume removes a named volume.
func (e *Engine) DeleteVolume(ctx context.Context, name string) error {
	_, err := e.Mutate(ctx, "volume", "delete", name)
	return err
}

// ListImages lists local images.
func (e *Engine) ListImages(ctx context.Context) ([]Image, error) {
	var out []Image
	err := e.JSON(ctx, &out, "image", "ls", "--format", "json")
	return out, err
}

// InspectImage returns an image or ErrNotFound.
func (e *Engine) InspectImage(ctx context.Context, ref string) (*Image, error) {
	var out []Image
	if err := e.JSON(ctx, &out, "image", "inspect", ref); err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// HasImage reports whether an image reference is present locally.
func (e *Engine) HasImage(ctx context.Context, ref string) (bool, error) {
	_, err := e.InspectImage(ctx, ref)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Pull fetches an image, streaming progress to the writers.
func (e *Engine) Pull(ctx context.Context, ref, platform string, stdout, stderr io.Writer) error {
	args := []string{"image", "pull"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, ref)
	code, err := e.Run(ctx, nil, stdout, stderr, args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return &ExitError{Args: args, Code: code, Stderr: "pull failed"}
	}
	return nil
}

// Build runs `container build args...` streaming output.
func (e *Engine) Build(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	full := append([]string{"build"}, args...)
	code, err := e.Run(ctx, nil, stdout, stderr, full...)
	if err != nil {
		return err
	}
	if code != 0 {
		return &ExitError{Args: full, Code: code, Stderr: "build failed"}
	}
	return nil
}

// DeleteImage removes an image reference.
func (e *Engine) DeleteImage(ctx context.Context, ref string) error {
	_, err := e.Mutate(ctx, "image", "delete", ref)
	return err
}

// Copy runs `container cp src dst`.
func (e *Engine) Copy(ctx context.Context, src, dst string) error {
	_, err := e.Mutate(ctx, "cp", src, dst)
	return err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AttachBackground is Attach for a process that must not receive the
// terminal's interrupt signal: apple-compose handles Ctrl-C itself and stops
// containers gracefully.
func (e *Engine) AttachBackground(ctx context.Context, id string, stdout, stderr io.Writer) (int, error) {
	if e.DryRun {
		e.echo("[dry-run]", []string{"start", "--attach", id})
		return 0, nil
	}
	cmd := e.Command(ctx, "start", "--attach", id)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	Detach(cmd)
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return xe.ExitCode(), nil
	}
	return -1, err
}
