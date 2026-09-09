package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
)

func (a *App) pullCommand() *cobra.Command {
	var quiet, ignoreBuildable, includeDeps, ignoreFailures bool
	var policy string
	cmd := &cobra.Command{
		Use:   "pull [OPTIONS] [SERVICE...]",
		Short: "Pull service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), args, includeDeps)
			if err != nil {
				return err
			}
			if err := a.eng.CheckRunning(cmd.Context()); err != nil {
				return err
			}
			r := a.runner(p)
			for _, name := range p.ServiceNames() {
				s, _ := p.GetService(name)
				if s.Build != nil && (ignoreBuildable || s.Image == "") {
					continue
				}
				if s.Image == "" {
					continue
				}
				pull := compose.PullAlways
				if policy == "missing" {
					pull = compose.PullMissing
				}
				if err := r.EnsureImage(cmd.Context(), s, compose.ImageOptions{Pull: pull, Quiet: quiet, NoBuild: true}); err != nil {
					if ignoreFailures {
						a.console.Warn("%v", err)
						continue
					}
					return err
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&quiet, "quiet", "q", false, "Pull without printing progress information")
	f.BoolVar(&ignoreBuildable, "ignore-buildable", false, "Ignore images that can be built")
	f.BoolVar(&includeDeps, "include-deps", false, "Also pull services declared as dependencies")
	f.BoolVar(&ignoreFailures, "ignore-pull-failures", false, "Pull what it can and ignores images with pull failures")
	f.StringVar(&policy, "policy", "always", `Apply pull policy ("missing"|"always")`)
	return cmd
}

func (a *App) pushCommand() *cobra.Command {
	var ignoreFailures, includeDeps, quiet bool
	cmd := &cobra.Command{
		Use:   "push [OPTIONS] [SERVICE...]",
		Short: "Push service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), args, includeDeps)
			if err != nil {
				return err
			}
			for _, name := range p.ServiceNames() {
				s, _ := p.GetService(name)
				if s.Image == "" {
					continue
				}
				if !quiet {
					a.console.Info(" %s Pushing %s", a.console.Paint("36", "⠿"), s.Image)
				}
				if _, err := a.eng.Run(cmd.Context(), nil, a.console.Err, a.console.Err, "image", "push", s.Image); err != nil {
					if ignoreFailures {
						a.console.Warn("%v", err)
						continue
					}
					return err
				}
				a.console.Step("Image", s.Image, "Pushed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&ignoreFailures, "ignore-push-failures", false, "Push what it can and ignores images with push failures")
	cmd.Flags().BoolVar(&includeDeps, "include-deps", false, "Also push images of services declared as dependencies")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Push without printing progress information")
	return cmd
}

func (a *App) buildCommand() *cobra.Command {
	var o compose.ImageOptions
	var buildArgs []string
	var withDeps bool
	cmd := &cobra.Command{
		Use:   "build [OPTIONS] [SERVICE...]",
		Short: "Build or rebuild services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), args, withDeps)
			if err != nil {
				return err
			}
			if err := a.eng.CheckRunning(cmd.Context()); err != nil {
				return err
			}
			r := a.runner(p)
			built := 0
			for _, name := range p.ServicesWithBuild() {
				s, _ := p.GetService(name)
				if len(args) > 0 && !containsArg(args, name) && !withDeps {
					continue
				}
				for _, kv := range buildArgs {
					k, v, _ := strings.Cut(kv, "=")
					if s.Build.Args == nil {
						s.Build.Args = map[string]*string{}
					}
					val := v
					s.Build.Args[k] = &val
				}
				if err := r.BuildService(cmd.Context(), s, o); err != nil {
					return err
				}
				built++
			}
			if built == 0 && len(args) > 0 {
				return fmt.Errorf("no service selected has a build section")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&buildArgs, "build-arg", nil, "Set build-time variables for services")
	f.BoolVar(&o.NoCache, "no-cache", false, "Do not use cache when building the image")
	f.BoolVar(&o.PullBuild, "pull", false, "Always attempt to pull a newer version of the image")
	f.BoolVarP(&o.Quiet, "quiet", "q", false, "Suppress the build output")
	f.BoolVar(&withDeps, "with-dependencies", false, "Also build dependencies (transitively)")
	f.String("progress", "auto", "Accepted for compatibility")
	_ = f.MarkHidden("progress")
	f.Bool("push", false, "Accepted for compatibility")
	_ = f.MarkHidden("push")
	return cmd
}

func (a *App) imagesCommand() *cobra.Command {
	var quiet bool
	var format string
	cmd := &cobra.Command{
		Use:   "images [OPTIONS] [SERVICE...]",
		Short: "List images used by the created containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProjectOrName(cmd.Context(), nil, false)
			if err != nil {
				return err
			}
			return a.runner(p).Images(cmd.Context(), args, quiet, format)
		},
	}
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only display IDs")
	cmd.Flags().StringVar(&format, "format", "table", `Format the output ("table"|"json")`)
	return cmd
}

func containsArg(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
