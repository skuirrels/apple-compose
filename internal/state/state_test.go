package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathsLiveUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	got, err := Home()
	if err != nil || got != home {
		t.Fatalf("Home = %q, %v", got, err)
	}
	if _, err := ProjectDir(""); err == nil {
		t.Fatal("an empty project name must be rejected")
	}
	hp, err := HostsPath("p", "p-web-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "projects", "p", "hosts", "p-web-1"); hp != want {
		t.Fatalf("HostsPath = %q, want %q", hp, want)
	}
	if st, err := os.Stat(filepath.Dir(hp)); err != nil || !st.IsDir() {
		t.Fatalf("hosts directory must be created: %v", err)
	}
	sd, err := SecretsDir("p")
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(sd); st.Mode().Perm() != 0o700 {
		t.Fatalf("secrets directory must be private, got %o", st.Mode().Perm())
	}
	vd, err := SharedVolumeDir("p_data")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "volumes", "p_data"); vd != want {
		t.Fatalf("SharedVolumeDir = %q, want %q", vd, want)
	}
	if err := RemoveProject("p"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "p")); !os.IsNotExist(err) {
		t.Fatal("RemoveProject must delete the project directory")
	}
	if err := RemoveSharedVolume("p_data"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(vd); !os.IsNotExist(err) {
		t.Fatal("RemoveSharedVolume must delete the directory")
	}
}

func TestDefaultHomeIsApplicationSupport(t *testing.T) {
	t.Setenv(EnvHome, "")
	t.Setenv("HOME", t.TempDir())
	got, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "apple-compose"); got != want {
		t.Fatalf("Home = %q, want %q", got, want)
	}
}
