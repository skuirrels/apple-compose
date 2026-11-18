package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/enginetest"
)

const cliProject = `
name: cliproj
services:
  web:
    image: alpine:3.20
    command: ["sleep", "100"]
`

// cliFixture points the CLI at a scripted runtime and a project of its own,
// so a command line can be followed all the way to the runtime arguments it
// produces.
func cliFixture(t *testing.T) (*enginetest.Fake, string) {
	t.Helper()
	f := enginetest.New(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(cliProject), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTAINER_BIN", f.Engine.Bin)
	t.Setenv("APPLE_COMPOSE_HOME", t.TempDir())
	t.Setenv("APPLE_COMPOSE_DNS", "runtime")
	t.Setenv("APPLE_COMPOSE_MEMORY", "runtime")
	t.Setenv("APPLE_COMPOSE_CPUS", "runtime")
	t.Setenv("APPLE_COMPOSE_IPV6_PORTS", "off")
	running := enginetest.ContainerJSON("cliproj-web-1", "web", "cliproj", "running", "10.0.0.2", "cliproj_default", nil)
	f.On("ls --format json", "["+running+"]", 0)
	f.On("inspect cliproj-web-1", "["+running+"]", 0)
	return f, dir
}

func TestCommandsReachTheRuntimeWithTheRightArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"stop passes the timeout", []string{"stop", "-t", "5"}, "stop --time 5 cliproj-web-1"},
		{"kill passes the signal", []string{"kill", "-s", "SIGINT"}, "kill --signal SIGINT cliproj-web-1"},
		{"rm forces removal", []string{"rm", "-f", "-s"}, "delete --force cliproj-web-1"},
		{"exec reaches the container", []string{"exec", "-T", "web", "true"}, "exec --interactive cliproj-web-1 true"},
		{"logs pass the tail", []string{"logs", "--tail", "7"}, "logs -n 7 cliproj-web-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, dir := cliFixture(t)
			if code := Execute("test", append([]string{"--project-directory", dir}, c.args...)); code != 0 {
				t.Fatalf("exit %d, calls %v", code, f.Calls())
			}
			if !f.Called(c.want) {
				t.Fatalf("expected a call starting %q, got %v", c.want, f.Calls())
			}
		})
	}
}

func TestScaleRejectsMalformedValues(t *testing.T) {
	_, dir := cliFixture(t)
	if code := Execute("test", []string{"--project-directory", dir, "up", "--scale", "web"}); code == 0 {
		t.Fatal("a --scale without a count must fail")
	}
	if code := Execute("test", []string{"--project-directory", dir, "up", "--scale", "web=many"}); code == 0 {
		t.Fatal("a --scale with a non-numeric count must fail")
	}
}

func TestPsQuietListsContainerIDs(t *testing.T) {
	_, dir := cliFixture(t)
	stdout := os.Stdout
	rf, wf, _ := os.Pipe()
	os.Stdout = wf
	code := Execute("test", []string{"--project-directory", dir, "ps", "-q"})
	wf.Close()
	os.Stdout = stdout
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := rf.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if code != 0 || strings.TrimSpace(out.String()) != "cliproj-web-1" {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
}
