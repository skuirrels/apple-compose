package dockercli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/template"
	"time"

	"github.com/skuirrels/apple-compose/internal/ui"
)

// templateFuncs mirrors the helpers Docker offers in --format templates.
var templateFuncs = template.FuncMap{
	"json": func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
	"join":  strings.Join,
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"title": func(s string) string {
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	"split":    strings.Split,
	"truncate": func(s string, n int) string { return truncate(s, n) },
	"pad": func(s string, left, right int) string {
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
	},
}

// render prints items the way Docker's --format does: a table by default,
// one JSON object per line for "json", or a Go template per item.
func render(w io.Writer, format string, items []any, headers []string, row func(any) []string) error {
	switch {
	case format == "" || format == "table":
		var rows [][]string
		for _, it := range items {
			rows = append(rows, row(it))
		}
		ui.Table(w, headers, rows)
		return nil
	case format == "json":
		enc := json.NewEncoder(w)
		for _, it := range items {
			if err := enc.Encode(it); err != nil {
				return err
			}
		}
		return nil
	case strings.HasPrefix(format, "table "):
		tmpl, err := template.New("row").Funcs(templateFuncs).Parse(strings.TrimPrefix(format, "table "))
		if err != nil {
			return fmt.Errorf("invalid --format: %w", err)
		}
		var rows [][]string
		for _, it := range items {
			var b strings.Builder
			if err := tmpl.Execute(&b, it); err != nil {
				return err
			}
			rows = append(rows, strings.Split(b.String(), "\t"))
		}
		var head []string
		for _, field := range strings.Split(strings.TrimPrefix(format, "table "), "\t") {
			head = append(head, strings.ToUpper(strings.Trim(strings.TrimSpace(field), "{}. ")))
		}
		ui.Table(w, head, rows)
		return nil
	default:
		tmpl, err := template.New("row").Funcs(templateFuncs).Parse(format)
		if err != nil {
			return fmt.Errorf("invalid --format: %w", err)
		}
		for _, it := range items {
			if err := tmpl.Execute(w, it); err != nil {
				return err
			}
			fmt.Fprintln(w)
		}
		return nil
	}
}

// renderInspect prints inspect results: a JSON array by default, or one
// template result per item.
func renderInspect(w io.Writer, format string, items []any) error {
	if format == "" || format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "    ")
		return enc.Encode(items)
	}
	tmpl, err := template.New("inspect").Funcs(templateFuncs).Parse(format)
	if err != nil {
		return fmt.Errorf("invalid --format: %w", err)
	}
	for _, it := range items {
		if err := tmpl.Execute(w, it); err != nil {
			return err
		}
		fmt.Fprintln(w)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// shortID renders an identifier the way Docker does: digests lose their
// prefix and everything is cut to twelve characters unless --no-trunc.
func shortID(id string, noTrunc bool) string {
	id = strings.TrimPrefix(id, "sha256:")
	if noTrunc {
		return id
	}
	return truncate(id, 12)
}

// humanDuration renders an age the way Docker does.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "Less than a second"
	case d < 2*time.Second:
		return "1 second"
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < 2*time.Minute:
		return "About a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 2*time.Hour:
		return "About an hour"
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d weeks", int(d.Hours()/(24*7)))
	case d < 2*365*24*time.Hour:
		return fmt.Sprintf("%d months", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%d years", int(d.Hours()/(24*365)))
	}
}

// humanSize renders bytes with Docker's units.
func humanSize(b int64) string {
	const unit = 1000.0
	if b < 1000 {
		return fmt.Sprintf("%dB", b)
	}
	v := float64(b)
	units := []string{"kB", "MB", "GB", "TB"}
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.3g%s", v, units[i])
}

// displayImage strips the docker.io prefixes Docker hides.
func displayImage(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "docker.io/library/"), "docker.io/")
}

// splitRef separates a reference into repository and tag.
func splitRef(ref string) (string, string) {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// parseFilters turns repeated --filter key=value flags into a map of lists.
func parseFilters(filters []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, f := range filters {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, fmt.Errorf("invalid filter %q, expected key=value", f)
		}
		out[k] = append(out[k], v)
	}
	return out, nil
}

// matchLabel applies Docker's label filter: "key" or "key=value".
func matchLabel(labels map[string]string, want string) bool {
	k, v, has := strings.Cut(want, "=")
	got, ok := labels[k]
	if !ok {
		return false
	}
	return !has || got == v
}
