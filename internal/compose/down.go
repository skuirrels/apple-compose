package compose

import (
	"context"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/state"
)

// DownOptions configure Down.
type DownOptions struct {
	Services      []string
	RemoveOrphans bool
	Volumes       bool
	RemoveImages  string // "all" or "local"
	Timeout       time.Duration
}

// Down stops and removes the project's containers, networks, and optionally
// volumes and images, mirroring `docker compose down`.
func (r *Runner) Down(ctx context.Context, o DownOptions) error {
	if err := r.Engine.CheckRunning(ctx); err != nil {
		return err
	}
	all, err := r.containers(ctx, true)
	if err != nil {
		return err
	}
	var targets []engine.Container
	known := map[string]bool{}
	for _, n := range r.Project.ServiceNames() {
		known[n] = true
	}
	for _, n := range r.Project.DisabledServiceNames() {
		known[n] = true
	}
	for _, c := range all {
		svc := c.Label(project.LabelService)
		switch {
		case len(o.Services) > 0:
			if containsString(o.Services, svc) {
				targets = append(targets, c)
			}
		case known[svc] || len(known) == 0 || o.RemoveOrphans || c.Label(project.LabelOneOff) == "True":
			targets = append(targets, c)
		}
	}
	targets = orderContainers(r.Project, targets, true)
	if err := r.stopContainers(ctx, targets, o.Timeout); err != nil {
		return err
	}
	if err := r.removeContainers(ctx, targets, true); err != nil {
		return err
	}
	if len(o.Services) > 0 {
		return nil
	}

	remaining, err := r.containers(ctx, true)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		if err := r.removeNetworks(ctx); err != nil {
			return err
		}
	}
	if o.Volumes {
		if err := r.removeVolumes(ctx); err != nil {
			return err
		}
	}
	if o.RemoveImages != "" {
		if err := r.removeImages(ctx, o.RemoveImages, targets); err != nil {
			return err
		}
	}
	if len(remaining) == 0 {
		_ = state.RemoveProject(r.Project.Name)
	}
	return nil
}

// removeNetworks deletes networks created for this project.
func (r *Runner) removeNetworks(ctx context.Context) error {
	nets, err := r.Engine.ListNetworks(ctx)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.Label(project.LabelProject) != r.Project.Name {
			continue
		}
		if key := n.Label(project.LabelNetwork); key != "" {
			if cfg, ok := r.Project.Networks[key]; ok && bool(cfg.External) {
				continue
			}
		}
		if err := r.Engine.DeleteNetwork(ctx, n.ID); err != nil {
			r.Console.Fail("Network", n.ID, "Removing", err)
			return err
		}
		r.Console.Step("Network", n.ID, "Removed")
	}
	return nil
}

// removeVolumes deletes named volumes created for this project.
func (r *Runner) removeVolumes(ctx context.Context) error {
	for _, key := range sortedKeys(r.Project.Volumes) {
		v := r.Project.Volumes[key]
		if ext, ok := v.Extensions[ExtensionKey].(map[string]any); ok {
			if shared, _ := ext["shared"].(bool); shared {
				name, _ := project.VolumeName(r.Project, key)
				if err := state.RemoveSharedVolume(name); err != nil {
					return err
				}
				r.Console.Step("Volume", name, "Removed")
			}
		}
	}
	vols, err := r.Engine.ListVolumes(ctx)
	if err != nil {
		return err
	}
	for _, v := range vols {
		if v.Label(project.LabelProject) != r.Project.Name {
			continue
		}
		if key := v.Label(project.LabelVolume); key != "" {
			if cfg, ok := r.Project.Volumes[key]; ok && bool(cfg.External) {
				continue
			}
		}
		if err := r.Engine.DeleteVolume(ctx, v.ID); err != nil {
			r.Console.Fail("Volume", v.ID, "Removing", err)
			return err
		}
		r.Console.Step("Volume", v.ID, "Removed")
	}
	return nil
}

// removeImages deletes images used by the removed containers: all of them,
// or only those built locally for the project.
func (r *Runner) removeImages(ctx context.Context, mode string, removed []engine.Container) error {
	seen := map[string]bool{}
	for _, c := range removed {
		svc, err := r.Project.GetService(c.Label(project.LabelService))
		image := c.Configuration.Image.Reference
		if err == nil {
			if mode == "local" && svc.Image != "" {
				continue
			}
			image = project.ImageName(r.Project, svc)
		} else if mode == "local" {
			continue
		}
		if seen[image] {
			continue
		}
		seen[image] = true
		if err := r.Engine.DeleteImage(ctx, image); err != nil {
			if engine.IsNotFound(err) {
				continue
			}
			r.Console.Fail("Image", image, "Removing", err)
			continue
		}
		r.Console.Step("Image", image, "Removed")
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
