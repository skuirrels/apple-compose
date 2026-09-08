package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
)

// EnsureNetworks creates the project's networks that do not exist yet and
// checks that external ones do.
func (r *Runner) EnsureNetworks(ctx context.Context) error {
	p := r.Project
	for _, key := range sortedKeys(p.Networks) {
		n := p.Networks[key]
		name, err := project.NetworkName(p, key)
		if err != nil {
			return err
		}
		existing, err := r.Engine.InspectNetwork(ctx, name)
		if err != nil && !errors.Is(err, engine.ErrNotFound) {
			return err
		}
		if existing != nil {
			continue
		}
		if bool(n.External) {
			return fmt.Errorf("network %s declared as external, but could not be found", name)
		}
		if n.Driver != "" && n.Driver != "bridge" && n.Driver != "default" {
			r.warnOnce("netdriver:"+key, "network %s: driver %q is not supported; the container runtime only offers vmnet networks", key, n.Driver)
		}
		opts := engine.NetworkOptions{Internal: n.Internal, Labels: map[string]string{
			project.LabelProject: p.Name,
			project.LabelNetwork: key,
			project.LabelVersion: r.Version,
		}}
		for k, v := range n.Labels {
			opts.Labels[k] = v
		}
		for _, pool := range n.Ipam.Config {
			if pool == nil || pool.Subnet == "" {
				continue
			}
			if strings.Contains(pool.Subnet, ":") {
				opts.SubnetV6 = pool.Subnet
			} else {
				opts.Subnet = pool.Subnet
			}
		}
		if err := r.Engine.CreateNetwork(ctx, name, opts); err != nil {
			r.Console.Fail("Network", name, "Creating", err)
			return err
		}
		r.Console.Step("Network", name, "Created")
	}
	return nil
}

// EnsureVolumes creates the project's named volumes that do not exist yet.
func (r *Runner) EnsureVolumes(ctx context.Context) error {
	p := r.Project
	for _, key := range sortedKeys(p.Volumes) {
		v := p.Volumes[key]
		name, err := project.VolumeName(p, key)
		if err != nil {
			return err
		}
		if _, bind, err := r.bindBackedVolume(key); err != nil {
			return err
		} else if bind {
			continue
		}
		existing, err := r.Engine.InspectVolume(ctx, name)
		if err != nil && !errors.Is(err, engine.ErrNotFound) {
			return err
		}
		if existing != nil {
			continue
		}
		if bool(v.External) {
			return fmt.Errorf("volume %s declared as external, but could not be found", name)
		}
		labels := map[string]string{
			project.LabelProject: p.Name,
			project.LabelVolume:  key,
			project.LabelVersion: r.Version,
		}
		for k, val := range v.Labels {
			labels[k] = val
		}
		opts := map[string]string{}
		for k, val := range v.DriverOpts {
			switch k {
			case "size", "journal":
				opts[k] = val
			default:
				r.warnOnce("volopt:"+key+k, "volume %s: driver option %q is not supported and is ignored", key, k)
			}
		}
		if err := r.Engine.CreateVolume(ctx, name, labels, opts); err != nil {
			r.Console.Fail("Volume", name, "Creating", err)
			return err
		}
		r.Console.Step("Volume", name, "Created")
	}
	return nil
}

// PullPolicy decides how an image is obtained before a container is created.
type PullPolicy string

// Pull policies, as in the compose specification.
const (
	PullDefault PullPolicy = ""
	PullAlways  PullPolicy = "always"
	PullMissing PullPolicy = "missing"
	PullNever   PullPolicy = "never"
	PullBuild   PullPolicy = "build"
)

// ImageOptions steer EnsureImage.
type ImageOptions struct {
	Build     bool // force a build for services with a build section
	NoBuild   bool // never build
	Pull      PullPolicy
	Quiet     bool
	NoCache   bool
	PullBuild bool
}

// EnsureImage makes the service's image available locally, building or
// pulling as its pull policy requires.
func (r *Runner) EnsureImage(ctx context.Context, s types.ServiceConfig, o ImageOptions) error {
	image := project.ImageName(r.Project, s)
	policy := o.Pull
	if policy == PullDefault {
		policy = PullPolicy(s.PullPolicy)
	}
	if policy == "if_not_present" {
		policy = PullMissing
	}
	present, err := r.Engine.HasImage(ctx, image)
	if err != nil {
		return err
	}
	if s.Build != nil {
		switch {
		case o.NoBuild:
			if !present {
				return fmt.Errorf("service %s: image %s is missing and --no-build was given", s.Name, image)
			}
			return nil
		case o.Build || policy == PullBuild || !present:
			return r.BuildService(ctx, s, o)
		default:
			return nil
		}
	}
	switch policy {
	case PullAlways:
		return r.pull(ctx, s, image, o.Quiet)
	case PullNever:
		if !present {
			return fmt.Errorf("service %s: image %s is not present locally and pull_policy is never", s.Name, image)
		}
		return nil
	default:
		if present {
			return nil
		}
		return r.pull(ctx, s, image, o.Quiet)
	}
}

func (r *Runner) pull(ctx context.Context, s types.ServiceConfig, image string, quiet bool) error {
	r.Console.Info(" %s Pulling %s", r.Console.Paint("36", "⠿"), image)
	var out io.Writer = r.Console.Err
	if quiet {
		out = discard
	}
	if err := r.Engine.Pull(ctx, image, s.Platform, out, out); err != nil {
		r.Console.Fail("Image", image, "Pulling", err)
		return err
	}
	r.Console.Step("Image", image, "Pulled")
	return nil
}

// BuildService builds a service's image with `container build`.
func (r *Runner) BuildService(ctx context.Context, s types.ServiceConfig, o ImageOptions) error {
	b := s.Build
	if b == nil {
		return nil
	}
	image := project.ImageName(r.Project, s)
	ctxDir := b.Context
	if ctxDir == "" {
		ctxDir = "."
	}
	if !filepath.IsAbs(ctxDir) {
		ctxDir = filepath.Join(r.Project.WorkingDir, ctxDir)
	}
	if strings.Contains(b.Context, "://") || strings.HasPrefix(b.Context, "git@") {
		return fmt.Errorf("service %s: remote build contexts are not supported", s.Name)
	}
	args := []string{"--tag", image}
	if b.Dockerfile != "" {
		df := b.Dockerfile
		if !filepath.IsAbs(df) {
			df = filepath.Join(ctxDir, df)
		}
		args = append(args, "--file", df)
	}
	if b.DockerfileInline != "" {
		return fmt.Errorf("service %s: dockerfile_inline is not supported; write a Dockerfile instead", s.Name)
	}
	for _, k := range sortedKeys(b.Args) {
		if v := b.Args[k]; v != nil {
			args = append(args, "--build-arg", k+"="+*v)
		}
	}
	if b.Target != "" {
		args = append(args, "--target", b.Target)
	}
	for _, platform := range b.Platforms {
		args = append(args, "--platform", platform)
	}
	if len(b.Platforms) == 0 && s.Platform != "" {
		args = append(args, "--platform", s.Platform)
	}
	if b.NoCache || o.NoCache {
		args = append(args, "--no-cache")
	}
	if b.Pull || o.PullBuild {
		args = append(args, "--pull")
	}
	for _, k := range sortedKeys(b.Labels) {
		args = append(args, "--label", k+"="+b.Labels[k])
	}
	for _, sec := range b.Secrets {
		src, err := r.fileObjectSource(types.FileObjectConfig(r.Project.Secrets[sec.Source]), sec.Source, "secret")
		if err != nil {
			return fmt.Errorf("service %s: build %w", s.Name, err)
		}
		id := sec.Target
		if id == "" {
			id = sec.Source
		}
		args = append(args, "--secret", "id="+id+",src="+src)
	}
	if len(b.SSH) > 0 {
		args = append(args, "--ssh", "default")
	}
	if b.ShmSize > 0 {
		r.warnOnce("build-shm:"+s.Name, "service %s: build.shm_size is ignored", s.Name)
	}
	if len(b.CacheFrom)+len(b.CacheTo) > 0 {
		r.warnOnce("build-cache:"+s.Name, "service %s: build cache_from/cache_to are ignored", s.Name)
	}
	if len(b.AdditionalContexts) > 0 {
		r.warnOnce("build-ctx:"+s.Name, "service %s: build.additional_contexts are ignored", s.Name)
	}
	for _, tag := range b.Tags {
		defer func(tag string) {
			_, _ = r.Engine.Mutate(ctx, "image", "tag", image, tag)
		}(tag)
	}
	if o.Quiet {
		args = append(args, "--quiet")
	}
	args = append(args, ctxDir)
	r.Console.Info(" %s Building %s", r.Console.Paint("36", "⠿"), s.Name)
	var out io.Writer = r.Console.Err
	if o.Quiet {
		out = discard
	}
	if err := r.Engine.Build(ctx, out, out, args...); err != nil {
		r.Console.Fail("Image", image, "Building", err)
		return err
	}
	r.Console.Step("Image", image, "Built")
	return nil
}
