// Package state locates the small amount of on-disk state apple-compose owns:
// the generated hosts files that are bind-mounted into service containers.
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvHome overrides the state directory.
const EnvHome = "APPLE_COMPOSE_HOME"

// Home returns the state directory, creating it if needed.
func Home() (string, error) {
	if p := os.Getenv(EnvHome); p != "" {
		return p, os.MkdirAll(p, 0o755)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(home, "Library", "Application Support", "apple-compose")
	return p, os.MkdirAll(p, 0o755)
}

// safeSegment rejects a name that would escape the directory it is joined to.
// Names reach here from the command line, so a container called ../../x must
// not place a generated file outside the state directory.
func safeSegment(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is empty", kind)
	}
	if name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return fmt.Errorf("invalid %s name %q: it must not contain a path", kind, name)
	}
	return nil
}

// ProjectDir returns the state directory of one project.
func ProjectDir(project string) (string, error) {
	if err := safeSegment("project", project); err != nil {
		return "", err
	}
	h, err := Home()
	if err != nil {
		return "", err
	}
	p := filepath.Join(h, "projects", project)
	return p, os.MkdirAll(p, 0o755)
}

// HostsDir returns the directory holding one project's hosts files.
func HostsDir(project string) (string, error) {
	p, err := ProjectDir(project)
	if err != nil {
		return "", err
	}
	h := filepath.Join(p, "hosts")
	return h, os.MkdirAll(h, 0o755)
}

// HostsPath returns the hosts file path for one container.
func HostsPath(project, container string) (string, error) {
	if err := safeSegment("container", container); err != nil {
		return "", err
	}
	d, err := HostsDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, container), nil
}

// SecretsDir returns the directory holding environment-backed secrets.
func SecretsDir(project string) (string, error) {
	p, err := ProjectDir(project)
	if err != nil {
		return "", err
	}
	s := filepath.Join(p, "secrets")
	return s, os.MkdirAll(s, 0o700)
}

// RemoveProject deletes all state for a project.
func RemoveProject(project string) error {
	if err := safeSegment("project", project); err != nil {
		return err
	}
	h, err := Home()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(h, "projects", project))
}

// SharedVolumeDir returns the host directory backing a shared volume.
func SharedVolumeDir(name string) (string, error) {
	if err := safeSegment("volume", name); err != nil {
		return "", err
	}
	h, err := Home()
	if err != nil {
		return "", err
	}
	p := filepath.Join(h, "volumes", name)
	return p, os.MkdirAll(p, 0o755)
}

// RemoveSharedVolume deletes a shared volume's directory.
func RemoveSharedVolume(name string) error {
	if err := safeSegment("volume", name); err != nil {
		return err
	}
	h, err := Home()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(h, "volumes", name))
}
