// Package project loads compose files into a compose-go Project the way
// Docker Compose does, so interpolation, .env handling, profiles, extends,
// include and multi-file merging behave identically.
package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
)

// Compose label keys shared with Docker Compose so both tools understand each
// other's resources.
const (
	LabelProject         = "com.docker.compose.project"
	LabelService         = "com.docker.compose.service"
	LabelContainerNumber = "com.docker.compose.container-number"
	LabelOneOff          = "com.docker.compose.oneoff"
	LabelConfigHash      = "com.docker.compose.config-hash"
	LabelWorkingDir      = "com.docker.compose.project.working_dir"
	LabelConfigFiles     = "com.docker.compose.project.config_files"
	LabelEnvironmentFile = "com.docker.compose.project.environment_file"
	LabelNetwork         = "com.docker.compose.network"
	LabelVolume          = "com.docker.compose.volume"
	LabelVersion         = "com.docker.compose.version"
	LabelDependsOn       = "com.docker.compose.depends_on"
	LabelReplace         = "com.docker.compose.replace"
	LabelImage           = "com.docker.compose.image"
	LabelSlug            = "com.docker.compose.slug"
	LabelManagedBy       = "com.apple-compose.managed"
)

// Options select which compose files to load and how.
type Options struct {
	ConfigPaths []string
	ProjectName string
	WorkingDir  string
	EnvFiles    []string
	Profiles    []string
	// AllServices includes services disabled by profiles.
	AllServices bool
}

// Load reads the project described by o.
func Load(ctx context.Context, o Options, version string) (*types.Project, error) {
	fns := []cli.ProjectOptionsFn{
		cli.WithOsEnv,
		cli.WithEnvFiles(o.EnvFiles...),
		cli.WithDotEnv,
		cli.WithConfigFileEnv,
		cli.WithDefaultConfigPath,
		cli.WithProfiles(o.Profiles),
		cli.WithInterpolation(true),
		cli.WithResolvedPaths(true),
		cli.WithNormalization(true),
		cli.WithConsistency(true),
	}
	if o.WorkingDir != "" {
		fns = append([]cli.ProjectOptionsFn{cli.WithWorkingDirectory(o.WorkingDir)}, fns...)
	}
	if o.ProjectName != "" {
		fns = append(fns, cli.WithName(o.ProjectName))
	}
	opts, err := cli.NewProjectOptions(o.ConfigPaths, fns...)
	if err != nil {
		return nil, err
	}
	p, err := opts.LoadProject(ctx)
	if err != nil {
		return nil, err
	}
	if o.AllServices {
		p, err = p.WithServicesEnabled(p.DisabledServiceNames()...)
		if err != nil {
			return nil, err
		}
	}
	for name, s := range p.Services {
		s.CustomLabels = map[string]string{
			LabelProject:     p.Name,
			LabelService:     name,
			LabelWorkingDir:  p.WorkingDir,
			LabelConfigFiles: strings.Join(p.ComposeFiles, ","),
			LabelOneOff:      "False",
			LabelVersion:     version,
		}
		if len(o.EnvFiles) > 0 {
			s.CustomLabels[LabelEnvironmentFile] = strings.Join(o.EnvFiles, ",")
		}
		p.Services[name] = s
	}
	return p, nil
}

// ContainerName returns the runtime id for a service replica.
func ContainerName(p *types.Project, s types.ServiceConfig, number int) string {
	if s.ContainerName != "" {
		return s.ContainerName
	}
	return fmt.Sprintf("%s-%s-%d", p.Name, s.Name, number)
}

// OneOffName returns the runtime id for a `run` container.
func OneOffName(p *types.Project, s types.ServiceConfig, slug string) string {
	return fmt.Sprintf("%s-%s-run-%s", p.Name, s.Name, slug)
}

// NetworkName returns the runtime name of a project network key.
func NetworkName(p *types.Project, key string) (string, error) {
	n, ok := p.Networks[key]
	if !ok {
		return "", fmt.Errorf("service references undefined network %q", key)
	}
	if n.Name != "" {
		return n.Name, nil
	}
	return p.Name + "_" + key, nil
}

// VolumeName returns the runtime name of a project volume key.
func VolumeName(p *types.Project, key string) (string, error) {
	v, ok := p.Volumes[key]
	if !ok {
		return "", fmt.Errorf("service references undefined volume %q", key)
	}
	if v.Name != "" {
		return v.Name, nil
	}
	return p.Name + "_" + key, nil
}

// ImageName returns the image a service runs, defaulting to the name Docker
// Compose gives images built from a service without an explicit image.
func ImageName(p *types.Project, s types.ServiceConfig) string {
	if s.Image != "" {
		return s.Image
	}
	return p.Name + "-" + s.Name
}

// RelPath renders a path relative to the working directory when possible.
func RelPath(p *types.Project, path string) string {
	if rel, err := filepath.Rel(p.WorkingDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// ServicesWithProfiles returns the services enabled after applying profiles,
// plus any named explicitly on the command line.
func Select(p *types.Project, names []string, withDeps bool) (*types.Project, error) {
	if len(names) == 0 {
		return p, nil
	}
	for _, n := range names {
		if _, err := p.GetService(n); err != nil {
			if _, derr := p.GetDisabledService(n); derr == nil {
				var err2 error
				p, err2 = p.WithServicesEnabled(n)
				if err2 != nil {
					return nil, err2
				}
				continue
			}
			return nil, fmt.Errorf("no such service: %s", n)
		}
	}
	if withDeps {
		return p.WithSelectedServices(names)
	}
	return p.WithSelectedServices(names, types.IgnoreDependencies)
}

// Exists reports whether a path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Service is an alias so callers need not import compose-go's types.
type Service = types.ServiceConfig
