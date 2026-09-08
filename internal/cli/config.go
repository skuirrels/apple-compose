package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/skuirrels/apple-compose/internal/compose"
	"github.com/skuirrels/apple-compose/internal/project"
)

func (a *App) configCommand() *cobra.Command {
	var (
		format                                       string
		services, volumes, networks, profiles, imgs  bool
		hash, output                                 string
		quiet, noInterpolate, noNormalize, noConsist bool
	)
	cmd := &cobra.Command{
		Use:     "config [OPTIONS] [SERVICE...]",
		Aliases: []string{"convert"},
		Short:   "Parse, resolve and render compose file in canonical format",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := a.loadProject(cmd.Context(), args, true)
			if err != nil {
				return err
			}
			out := os.Stdout
			if output != "" {
				f, err := os.Create(output)
				if err != nil {
					return err
				}
				defer f.Close()
				out = f
			}
			switch {
			case quiet:
				return nil
			case services:
				for _, s := range p.ServiceNames() {
					fmt.Fprintln(out, s)
				}
			case volumes:
				for _, v := range p.VolumeNames() {
					fmt.Fprintln(out, v)
				}
			case networks:
				for _, n := range p.NetworkNames() {
					fmt.Fprintln(out, n)
				}
			case profiles:
				set := map[string]bool{}
				for _, s := range p.AllServices() {
					for _, pr := range s.Profiles {
						set[pr] = true
					}
				}
				names := make([]string, 0, len(set))
				for n := range set {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					fmt.Fprintln(out, n)
				}
			case imgs:
				for _, name := range p.ServiceNames() {
					s, _ := p.GetService(name)
					fmt.Fprintln(out, project.ImageName(p, s))
				}
			case hash != "":
				for _, name := range p.ServiceNames() {
					if hash != "*" && !containsArg(splitComma(hash), name) {
						continue
					}
					s, _ := p.GetService(name)
					h, err := compose.ServiceHash(s)
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "%s %s\n", name, h)
				}
			case format == "json":
				b, err := p.MarshalJSON()
				if err != nil {
					return err
				}
				var buf map[string]any
				_ = json.Unmarshal(b, &buf)
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(buf)
			default:
				b, err := p.MarshalYAML()
				if err != nil {
					return err
				}
				_, err = out.Write(b)
				return err
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&format, "format", "yaml", `Format the output ("yaml"|"json")`)
	f.BoolVar(&services, "services", false, "Print the service names, one per line")
	f.BoolVar(&volumes, "volumes", false, "Print the volume names, one per line")
	f.BoolVar(&networks, "networks", false, "Print the network names, one per line")
	f.BoolVar(&profiles, "profiles", false, "Print the profile names, one per line")
	f.BoolVar(&imgs, "images", false, "Print the image names, one per line")
	f.StringVar(&hash, "hash", "", `Print the service config hash, one per line. Set "service1,service2" or "*"`)
	f.StringVarP(&output, "output", "o", "", "Save to file (default to stdout)")
	f.BoolVarP(&quiet, "quiet", "q", false, "Only validate the configuration, don't print anything")
	f.BoolVar(&noInterpolate, "no-interpolate", false, "Accepted for compatibility")
	f.BoolVar(&noNormalize, "no-normalize", false, "Accepted for compatibility")
	f.BoolVar(&noConsist, "no-consistency", false, "Accepted for compatibility")
	_ = f.MarkHidden("no-interpolate")
	_ = f.MarkHidden("no-normalize")
	_ = f.MarkHidden("no-consistency")
	return cmd
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	return append(out, cur)
}
