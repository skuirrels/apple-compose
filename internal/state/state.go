// Package state locates the small amount of on-disk state apple-compose owns:
// the generated hosts files that are bind-mounted into service containers.
package state

import (
	"errors"
	"os"
	"path/filepath"
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

// ProjectDir returns the state directory of one project.
func ProjectDir(project string) (string, error) {
	if project == "" {
		return "", errors.New("project name is empty")
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
	h, err := Home()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(h, "projects", project))
}

// SharedVolumeDir returns the host directory backing a shared volume.
func SharedVolumeDir(name string) (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	p := filepath.Join(h, "volumes", name)
	return p, os.MkdirAll(p, 0o755)
}

// RemoveSharedVolume deletes a shared volume's directory.
func RemoveSharedVolume(name string) error {
	h, err := Home()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(h, "volumes", name))
}
