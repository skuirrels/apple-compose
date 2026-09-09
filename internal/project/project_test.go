package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAppliesEnvOverridesAndLabels(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "TAG=3.20\n")
	write(t, dir, "compose.yaml", `
services:
  web:
    image: alpine:${TAG}
    profiles: [web]
  db:
    image: alpine:${TAG}
`)
	write(t, dir, "compose.override.yaml", "services:\n  db:\n    environment:\n      X: \"1\"\n")
	p, err := Load(context.Background(), Options{WorkingDir: dir, Profiles: []string{"web"}}, "v")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != filepath.Base(dir) {
		t.Errorf("project name = %q", p.Name)
	}
	db, _ := p.GetService("db")
	if db.Image != "alpine:3.20" || db.Environment["X"] == nil || *db.Environment["X"] != "1" {
		t.Errorf("interpolation or override not applied: %+v", db)
	}
	if db.CustomLabels[LabelProject] != p.Name || db.CustomLabels[LabelService] != "db" || db.CustomLabels[LabelVersion] != "v" {
		t.Errorf("labels = %v", db.CustomLabels)
	}
	if _, err := p.GetService("web"); err != nil {
		t.Errorf("profile web should be enabled: %v", err)
	}
	if len(p.ComposeFiles) != 2 {
		t.Errorf("expected override to be merged, files = %v", p.ComposeFiles)
	}
}

func TestSelectAndProfiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `
name: sel
services:
  web:
    image: img
    depends_on: [db]
  db:
    image: img
  debug:
    image: img
    profiles: [debug]
`)
	p, err := Load(context.Background(), Options{WorkingDir: dir}, "v")
	if err != nil {
		t.Fatal(err)
	}
	if p.DisabledServiceNames()[0] != "debug" {
		t.Errorf("debug should be disabled: %v", p.DisabledServiceNames())
	}
	sel, err := Select(p, []string{"web"}, true)
	if err != nil || len(sel.Services) != 2 {
		t.Errorf("select with deps: %v %v", sel.ServiceNames(), err)
	}
	sel, err = Select(p, []string{"web"}, false)
	if err != nil || len(sel.Services) != 1 {
		t.Errorf("select without deps: %v %v", sel.ServiceNames(), err)
	}
	sel, err = Select(p, []string{"debug"}, false)
	if err != nil || sel.Services["debug"].Name != "debug" {
		t.Errorf("selecting a profiled service must enable it: %v", err)
	}
	if _, err := Select(p, []string{"nope"}, false); err == nil {
		t.Error("unknown service must error")
	}
	all, err := Load(context.Background(), Options{WorkingDir: dir, AllServices: true}, "v")
	if err != nil || len(all.Services) != 3 {
		t.Errorf("AllServices: %v %v", all.ServiceNames(), err)
	}
}

func TestNames(t *testing.T) {
	p := &types.Project{Name: "proj", Networks: types.Networks{"default": {}, "named": {Name: "custom"}}, Volumes: types.Volumes{"data": {}, "ext": {Name: "real"}}}
	if ContainerName(p, types.ServiceConfig{Name: "web"}, 2) != "proj-web-2" {
		t.Error("container name")
	}
	if ContainerName(p, types.ServiceConfig{Name: "web", ContainerName: "fixed"}, 1) != "fixed" {
		t.Error("container_name override")
	}
	if n, _ := NetworkName(p, "default"); n != "proj_default" {
		t.Error("network name")
	}
	if n, _ := NetworkName(p, "named"); n != "custom" {
		t.Error("explicit network name")
	}
	if _, err := NetworkName(p, "missing"); err == nil {
		t.Error("undefined network must error")
	}
	if v, _ := VolumeName(p, "data"); v != "proj_data" {
		t.Error("volume name")
	}
	if v, _ := VolumeName(p, "ext"); v != "real" {
		t.Error("explicit volume name")
	}
	if ImageName(p, types.ServiceConfig{Name: "api"}) != "proj-api" || ImageName(p, types.ServiceConfig{Image: "x:1"}) != "x:1" {
		t.Error("image name")
	}
	if OneOffName(p, types.ServiceConfig{Name: "api"}, "abc") != "proj-api-run-abc" {
		t.Error("one-off name")
	}
}
