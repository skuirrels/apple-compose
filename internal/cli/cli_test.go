package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestNeedsRuntime(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	completion := &cobra.Command{Use: "completion"}
	bash := &cobra.Command{Use: "bash"}
	completion.AddCommand(bash)
	up := &cobra.Command{Use: "up"}
	root.AddCommand(completion, up)
	if needsRuntime(bash) || needsRuntime(completion) {
		t.Error("completion must not need the runtime")
	}
	if !needsRuntime(up) {
		t.Error("up needs the runtime")
	}
}

func TestConfigAndVersionWorkWithoutRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("name: cfg\nservices:\n  web:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTAINER_BIN", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())
	stdout := os.Stdout
	rf, wf, _ := os.Pipe()
	os.Stdout = wf
	code := Execute("9.9.9", []string{"--project-directory", dir, "config", "--services"})
	wf.Close()
	os.Stdout = stdout
	var buf bytes.Buffer
	buf.ReadFrom(rf)
	if code != 0 || buf.String() != "web\n" {
		t.Fatalf("config --services: code %d, out %q", code, buf.String())
	}
	if code := Execute("9.9.9", []string{"up", "--scale", "web=x"}); code == 0 {
		t.Fatal("bad --scale must fail")
	}
}
