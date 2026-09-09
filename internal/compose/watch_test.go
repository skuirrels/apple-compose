package compose

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestWatchIgnoreAndInclude(t *testing.T) {
	rule := watchRule{ignore: []string{"node_modules", "*.tmp", "build/cache", "dist/"}}
	for _, p := range []string{"node_modules/x/y.js", "a/node_modules/y.js", "notes.tmp", "build/cache/out", "dist/app.js", ".git/config"} {
		if !watchIgnores(rule, filepath.FromSlash(p), false) {
			t.Errorf("%s must be ignored", p)
		}
	}
	for _, p := range []string{"src/main.go", "build/main.go", "tmp/x"} {
		if watchIgnores(rule, filepath.FromSlash(p), false) {
			t.Errorf("%s must not be ignored", p)
		}
	}
	// Include narrows to matching files, but never prunes a directory: the
	// files beneath it may match.
	rule = watchRule{include: []string{"*.go"}}
	if watchIgnores(rule, "main.go", false) || !watchIgnores(rule, "main.js", false) {
		t.Fatal("include must keep only matching files")
	}
	if watchIgnores(rule, "pkg", true) {
		t.Fatal("include must not prune directories")
	}
}

func TestDiffTrees(t *testing.T) {
	before := map[string]stamp{"a": {size: 1}, "b": {size: 2}}
	after := map[string]stamp{"a": {size: 1}, "b": {size: 3}, "c": {size: 4}}
	changed, deleted := diffTrees(before, after)
	if !reflect.DeepEqual(changed, []string{"b", "c"}) || len(deleted) != 0 {
		t.Fatalf("got %v %v", changed, deleted)
	}
	changed, deleted = diffTrees(after, before)
	if !reflect.DeepEqual(changed, []string{"b"}) || !reflect.DeepEqual(deleted, []string{"c"}) {
		t.Fatalf("got %v %v", changed, deleted)
	}
}

func TestScanTreeSkipsIgnoredDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"src/main.go", "src/util.go", "node_modules/dep/index.js", ".git/HEAD", "src/notes.tmp"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := scanTree(watchRule{root: dir, ignore: []string{"node_modules", "*.tmp"}})
	want := []string{filepath.Join(dir, "src", "main.go"), filepath.Join(dir, "src", "util.go")}
	if len(got) != len(want) {
		t.Fatalf("scanned %v, want %v", sortedPaths(got), want)
	}
	for _, p := range want {
		if _, ok := got[p]; !ok {
			t.Fatalf("missing %s in %v", p, sortedPaths(got))
		}
	}
}

func TestWatchTargetMapsPaths(t *testing.T) {
	rule := watchRule{root: filepath.FromSlash("/host/src"), target: "/app"}
	got, err := watchTarget(rule, filepath.FromSlash("/host/src/pkg/main.go"))
	if err != nil || got != "/app/pkg/main.go" {
		t.Fatalf("tree target = %q %v", got, err)
	}
	rule = watchRule{root: filepath.FromSlash("/host/config.json"), target: "/etc/app/config.json", file: true}
	if got, _ := watchTarget(rule, filepath.FromSlash("/host/config.json")); got != "/etc/app/config.json" {
		t.Fatalf("file target = %q", got)
	}
}

func TestWatchRulesValidate(t *testing.T) {
	// compose-go resolves a trigger path against the compose file, so the
	// fixture puts the watched tree beside it.
	files := map[string]string{"src/main.go": "package main"}
	if _, err := runnerRules(t, files, `
name: t
services:
  web:
    image: img
    develop:
      watch:
        - path: ./missing
          action: rebuild
`); err == nil {
		t.Fatal("a trigger on a missing path must be rejected")
	}
	rules, err := runnerRules(t, files, `
name: t
services:
  web:
    image: img
    develop:
      watch:
        - path: ./src
          action: sync+restart
          target: /app
          ignore: [tmp]
        - path: ./src/main.go
          action: sync
          target: /app/main.go
`)
	if err != nil || len(rules) != 2 {
		t.Fatalf("rules = %+v %v", rules, err)
	}
	if rules[0].service != "web" || rules[0].action != types.WatchActionSyncRestart || rules[0].target != "/app" || rules[0].file {
		t.Fatalf("unexpected tree rule %+v", rules[0])
	}
	if !rules[1].file {
		t.Fatal("a trigger on a single file must be recognised as one")
	}
	if filepath.Base(rules[0].root) != "src" {
		t.Fatalf("root = %s", rules[0].root)
	}
}

// runnerRules loads a fixture project and resolves its watch triggers.
func runnerRules(t *testing.T, files map[string]string, yaml string) ([]watchRule, error) {
	t.Helper()
	r, _ := loadRunner(t, yaml, files)
	return r.watchRules(nil)
}

func TestWatchRulesFilterServices(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  web:
    image: img
    develop:
      watch:
        - path: ./src
          action: rebuild
  db:
    image: img
`, map[string]string{"src/main.go": "package main"})
	if rules, err := r.watchRules([]string{"db"}); err != nil || len(rules) != 0 {
		t.Fatalf("a service filter must exclude other services: %v %v", rules, err)
	}
	if rules, err := r.watchRules([]string{"web"}); err != nil || len(rules) != 1 {
		t.Fatalf("the named service must be watched: %v %v", rules, err)
	}
}
