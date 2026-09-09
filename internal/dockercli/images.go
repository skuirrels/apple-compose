package dockercli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/engine"
)

// imageRow is the shape Docker's `images --format json` prints.
type imageRow struct {
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	ID           string `json:"ID"`
	Digest       string `json:"Digest"`
	CreatedAt    string `json:"CreatedAt"`
	CreatedSince string `json:"CreatedSince"`
	Size         string `json:"Size"`
	Containers   string `json:"Containers"`
	VirtualSize  string `json:"VirtualSize"`
}

func imageSize(img engine.Image) int64 {
	var total int64
	for _, v := range img.Variants {
		total += v.Size
	}
	if total == 0 {
		total = img.Configuration.Descriptor.Size
	}
	return total
}

func toImageRow(img engine.Image, noTrunc bool) imageRow {
	repo, tag := splitRef(displayImage(img.Name()))
	return imageRow{
		Repository:   repo,
		Tag:          tag,
		ID:           shortID(img.ID, noTrunc),
		Digest:       img.Configuration.Descriptor.Digest,
		CreatedAt:    img.Configuration.CreationDate.Local().Format("2006-01-02 15:04:05 -0700 MST"),
		CreatedSince: humanDuration(time.Since(img.Configuration.CreationDate)) + " ago",
		Size:         humanSize(imageSize(img)),
		Containers:   "N/A",
		VirtualSize:  humanSize(imageSize(img)),
	}
}

// listImages applies Docker's image filters.
func (a *App) listImages(ctx context.Context, filters map[string][]string) ([]engine.Image, error) {
	imgs, err := a.eng.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	var out []engine.Image
	for _, img := range imgs {
		if !matchImage(img, filters) {
			continue
		}
		out = append(out, img)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Configuration.CreationDate.After(out[j].Configuration.CreationDate)
	})
	return out, nil
}

func matchImage(img engine.Image, filters map[string][]string) bool {
	for key, values := range filters {
		ok := false
		for _, v := range values {
			switch key {
			case "reference":
				name := displayImage(img.Name())
				repo, _ := splitRef(name)
				ok = ok || name == v || repo == v || strings.HasPrefix(name, v) || globMatch(v, name)
			case "dangling":
				ok = ok || v == "false"
			case "before", "since", "label":
				ok = true
			default:
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// globMatch supports the `*` wildcard Docker accepts in reference filters.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

func (a *App) imagesCommand() *cobra.Command {
	var all, quiet, noTrunc, digests bool
	var format string
	var filters []string
	cmd := &cobra.Command{
		Use:   "images [OPTIONS] [REPOSITORY[:TAG]]",
		Short: "List images",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = all
			fl, err := parseFilters(filters)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				fl["reference"] = append(fl["reference"], args[0])
			}
			imgs, err := a.listImages(cmd.Context(), fl)
			if err != nil {
				return err
			}
			if quiet {
				for _, img := range imgs {
					fmt.Fprintln(a.console.Out, shortID(img.ID, noTrunc))
				}
				return nil
			}
			items := make([]any, 0, len(imgs))
			for _, img := range imgs {
				items = append(items, toImageRow(img, noTrunc))
			}
			headers := []string{"REPOSITORY", "TAG", "IMAGE ID", "CREATED", "SIZE"}
			if digests {
				headers = []string{"REPOSITORY", "TAG", "DIGEST", "IMAGE ID", "CREATED", "SIZE"}
			}
			return render(a.console.Out, format, items, headers, func(it any) []string {
				r := it.(imageRow)
				if digests {
					return []string{r.Repository, r.Tag, r.Digest, r.ID, r.CreatedSince, r.Size}
				}
				return []string{r.Repository, r.Tag, r.ID, r.CreatedSince, r.Size}
			})
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&all, "all", "a", false, "Show all images (the runtime has no intermediate images)")
	f.BoolVarP(&quiet, "quiet", "q", false, "Only show image IDs")
	f.BoolVar(&noTrunc, "no-trunc", false, "Don't truncate output")
	f.BoolVar(&digests, "digests", false, "Show digests")
	f.StringVar(&format, "format", "", `Format output using a custom template: 'table', 'table TEMPLATE', 'json', or a Go template`)
	f.StringArrayVarP(&filters, "filter", "f", nil, "Filter output based on conditions provided")
	return cmd
}

func (a *App) pullCommand() *cobra.Command {
	var platform string
	var quiet, allTags bool
	cmd := &cobra.Command{
		Use:   "pull [OPTIONS] NAME[:TAG|@DIGEST]",
		Short: "Download an image from a registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if allTags {
				return fmt.Errorf("--all-tags is not supported by the container runtime")
			}
			pargs := []string{"image", "pull"}
			if platform != "" {
				pargs = append(pargs, "--platform", platform)
			}
			if quiet {
				pargs = append(pargs, "--progress", "none")
			}
			a.ignored(cmd, "disable-content-trust")
			return a.exec(append(pargs, args[0])...)
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "Set platform if server is multi-platform capable")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress verbose output")
	cmd.Flags().BoolVarP(&allTags, "all-tags", "a", false, "Download all tagged images in the repository")
	cmd.Flags().Bool("disable-content-trust", true, "Accepted for compatibility with Docker; ignored")
	_ = cmd.Flags().MarkHidden("disable-content-trust")
	return cmd
}

func (a *App) pushCommand() *cobra.Command {
	var platform string
	var quiet, allTags bool
	cmd := &cobra.Command{
		Use:   "push [OPTIONS] NAME[:TAG]",
		Short: "Upload an image to a registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if allTags {
				return fmt.Errorf("--all-tags is not supported by the container runtime")
			}
			pargs := []string{"image", "push"}
			if platform != "" {
				pargs = append(pargs, "--platform", platform)
			}
			if quiet {
				pargs = append(pargs, "--progress", "none")
			}
			return a.exec(append(pargs, args[0])...)
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "Push a platform-specific manifest as a single-platform image")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress verbose output")
	cmd.Flags().BoolVarP(&allTags, "all-tags", "a", false, "Push all tags of an image")
	return cmd
}

func (a *App) tagCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tag SOURCE_IMAGE[:TAG] TARGET_IMAGE[:TAG]",
		Short: "Create a tag TARGET_IMAGE that refers to SOURCE_IMAGE",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := a.eng.Mutate(cmd.Context(), "image", "tag", args[0], args[1])
			return err
		},
	}
}

func (a *App) rmiCommand() *cobra.Command {
	var force, noPrune bool
	cmd := &cobra.Command{
		Use:   "rmi [OPTIONS] IMAGE [IMAGE...]",
		Short: "Remove one or more images",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = noPrune
			var errs int
			for _, ref := range args {
				dargs := []string{"image", "delete"}
				if force {
					dargs = append(dargs, "--force")
				}
				if _, err := a.eng.Mutate(cmd.Context(), append(dargs, ref)...); err != nil {
					fmt.Fprintf(a.console.Err, "Error response from daemon: %v\n", err)
					errs++
					continue
				}
				fmt.Fprintf(a.console.Out, "Untagged: %s\n", ref)
			}
			if errs > 0 {
				return ExitCodeError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force removal of the image")
	cmd.Flags().BoolVar(&noPrune, "no-prune", false, "Accepted for compatibility with Docker; ignored")
	_ = cmd.Flags().MarkHidden("no-prune")
	return cmd
}

func (a *App) historyCommand() *cobra.Command {
	return a.unsupported("history", "Show the history of an image", "the container runtime does not expose layer history; use `image inspect`")
}

// ignoredBuildFlags are Docker build options the runtime cannot honour.
var ignoredBuildFlags = []string{"cache-from", "cache-to", "network", "squash", "iidfile", "add-host", "shm-size", "memory", "cpu-shares", "cpu-period", "cpu-quota", "cpuset-cpus", "cpuset-mems", "isolation", "security-opt", "ulimit", "compress", "force-rm", "rm", "builder", "attest", "provenance", "sbom", "load", "push", "metadata-file", "annotation", "allow", "call", "check"}

func (a *App) buildCommand() *cobra.Command {
	var (
		tags, buildArgs, labels, secrets, platforms, sshs []string
		file, target, progress, output                    string
		noCache, pull, quiet                              bool
	)
	cmd := &cobra.Command{
		Use:   "build [OPTIONS] PATH | URL | -",
		Short: "Build an image from a Dockerfile",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bargs := []string{"build"}
			for _, t := range tags {
				bargs = append(bargs, "--tag", t)
			}
			if file != "" {
				bargs = append(bargs, "--file", file)
			}
			for _, b := range buildArgs {
				bargs = append(bargs, "--build-arg", b)
			}
			for _, l := range labels {
				bargs = append(bargs, "--label", l)
			}
			for _, s := range secrets {
				bargs = append(bargs, "--secret", s)
			}
			for _, s := range sshs {
				bargs = append(bargs, "--ssh", s)
			}
			for _, p := range platforms {
				for _, single := range strings.Split(p, ",") {
					bargs = append(bargs, "--platform", single)
				}
			}
			if target != "" {
				bargs = append(bargs, "--target", target)
			}
			if noCache {
				bargs = append(bargs, "--no-cache")
			}
			if pull {
				bargs = append(bargs, "--pull")
			}
			if output != "" {
				bargs = append(bargs, "--output", output)
			}
			for _, d := range engine.DefaultDNS() {
				bargs = append(bargs, "--dns", d)
			}
			switch {
			case quiet:
				// The runtime's --quiet hangs on 1.3.1; plain progress is
				// the closest quiet output that completes.
				bargs = append(bargs, "--progress", "plain")
			case progress != "" && progress != "auto":
				bargs = append(bargs, "--progress", progress)
			}
			a.ignored(cmd, ignoredBuildFlags...)
			context := "."
			if len(args) == 1 {
				context = args[0]
			}
			if context == "-" {
				return fmt.Errorf("building from stdin is not supported by the container runtime")
			}
			return a.exec(append(bargs, context)...)
		},
	}
	f := cmd.Flags()
	f.StringArrayVarP(&tags, "tag", "t", nil, "Name and optionally a tag in the name:tag format")
	f.StringVarP(&file, "file", "f", "", "Name of the Dockerfile")
	f.StringArrayVar(&buildArgs, "build-arg", nil, "Set build-time variables")
	f.StringArrayVar(&labels, "label", nil, "Set metadata for an image")
	f.StringArrayVar(&secrets, "secret", nil, "Secret to expose to the build")
	f.StringArrayVar(&sshs, "ssh", nil, "SSH agent socket or keys to expose to the build")
	f.StringArrayVar(&platforms, "platform", nil, "Set target platform for build")
	f.StringVar(&target, "target", "", "Set the target build stage to build")
	f.StringVar(&progress, "progress", "auto", "Set type of progress output (auto, plain, tty)")
	f.StringVarP(&output, "output", "o", "", "Output destination")
	f.BoolVar(&noCache, "no-cache", false, "Do not use cache when building the image")
	f.BoolVar(&pull, "pull", false, "Always attempt to pull a newer version of the image")
	f.BoolVarP(&quiet, "quiet", "q", false, "Suppress the build output")
	for _, n := range ignoredBuildFlags {
		f.StringArray(n, nil, "Accepted for compatibility with Docker; ignored")
		_ = f.MarkHidden(n)
	}
	return cmd
}

// imageInspectView is a Docker-shaped image record.
type imageInspectView struct {
	Id           string `json:"Id"`
	RepoTags     []string
	RepoDigests  []string
	Created      string
	Architecture string
	Os           string
	Size         int64
	Config       struct {
		Cmd          []string
		Entrypoint   []string
		Env          []string
		WorkingDir   string
		User         string
		ExposedPorts map[string]any
		StopSignal   string `json:",omitempty"`
	}
	Runtime engine.Image `json:"Runtime"`
}

func toImageInspect(img engine.Image) imageInspectView {
	v := imageInspectView{Id: "sha256:" + strings.TrimPrefix(img.ID, "sha256:"), RepoTags: []string{displayImage(img.Name())}, Created: img.Configuration.CreationDate.UTC().Format(time.RFC3339Nano), Size: imageSize(img), Runtime: img}
	if d := img.Configuration.Descriptor.Digest; d != "" {
		repo, _ := splitRef(displayImage(img.Name()))
		v.RepoDigests = []string{repo + "@" + d}
	}
	for _, variant := range img.Variants {
		if variant.Platform.Architecture != "" && variant.Platform.Architecture != "arm64" {
			continue
		}
		v.Architecture = variant.Platform.Architecture
		v.Os = variant.Platform.OS
		v.Config.Cmd = variant.Config.Config.Cmd
		v.Config.Entrypoint = variant.Config.Config.Entrypoint
		v.Config.Env = variant.Config.Config.Env
		v.Config.WorkingDir = variant.Config.Config.WorkingDir
		v.Config.User = variant.Config.Config.User
		v.Config.ExposedPorts = variant.Config.Config.ExposedPorts
		v.Config.StopSignal = variant.Config.Config.StopSignal
		break
	}
	return v
}

// imageCommand groups the `docker image ...` spellings.
func (a *App) imageCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "image", Short: "Manage images"}
	ls := a.imagesCommand()
	ls.Use = "ls [OPTIONS] [REPOSITORY[:TAG]]"
	ls.Aliases = []string{"list", "images"}
	rm := a.rmiCommand()
	rm.Use = "rm [OPTIONS] IMAGE [IMAGE...]"
	rm.Aliases = []string{"remove", "rmi"}

	var format string
	inspect := &cobra.Command{
		Use:   "inspect [OPTIONS] IMAGE [IMAGE...]",
		Short: "Display detailed information on one or more images",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := a.inspectAny(cmd.Context(), args, "image")
			if err != nil {
				return err
			}
			return renderInspect(a.console.Out, format, items)
		},
	}
	inspect.Flags().StringVarP(&format, "format", "f", "", "Format output using a custom template")

	var all, force bool
	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused images",
		RunE: func(cmd *cobra.Command, args []string) error {
			msg := "WARNING! This will remove all dangling images.\nAre you sure you want to continue?"
			if all {
				msg = "WARNING! This will remove all images without at least one container associated to them.\nAre you sure you want to continue?"
			}
			if !force && !a.confirm(msg) {
				return nil
			}
			pargs := []string{"image", "prune"}
			if all {
				pargs = append(pargs, "--all")
			}
			return a.runAttached(cmd.Context(), pargs...)
		},
	}
	prune.Flags().BoolVarP(&all, "all", "a", false, "Remove all unused images, not just dangling ones")
	prune.Flags().BoolVarP(&force, "force", "f", false, "Do not prompt for confirmation")

	var output, input string
	var loadQuiet bool
	save := &cobra.Command{
		Use:   "save [OPTIONS] IMAGE [IMAGE...]",
		Short: "Save one or more images to a tar archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sargs := []string{"image", "save"}
			if output != "" {
				sargs = append(sargs, "--output", output)
			}
			return a.exec(append(sargs, args[0])...)
		},
	}
	save.Flags().StringVarP(&output, "output", "o", "", "Write to a file, instead of STDOUT")
	load := &cobra.Command{
		Use:   "load [OPTIONS]",
		Short: "Load an image from a tar archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" {
				return fmt.Errorf("--input is required: the container runtime cannot load from stdin")
			}
			_ = loadQuiet
			return a.exec("image", "load", "--input", input)
		},
	}
	load.Flags().StringVarP(&input, "input", "i", "", "Read from tar archive file")
	load.Flags().BoolVarP(&loadQuiet, "quiet", "q", false, "Accepted for compatibility with Docker; ignored")

	cmd.AddCommand(ls, rm, inspect, prune, save, load, a.pullCommand(), a.pushCommand(), a.tagCommand(), a.buildCommand(), a.historyCommand())
	return cmd
}
