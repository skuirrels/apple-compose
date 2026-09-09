package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// PsOptions configure Ps.
type PsOptions struct {
	Services     []string
	All          bool
	Status       []string
	Quiet        bool
	ServicesOnly bool
	Format       string
	Orphans      bool
	Health       bool
}

// PsRow is one container as reported by `ps --format json`.
type PsRow struct {
	Name      string            `json:"Name"`
	Service   string            `json:"Service"`
	Image     string            `json:"Image"`
	Command   string            `json:"Command"`
	Project   string            `json:"Project"`
	State     string            `json:"State"`
	Status    string            `json:"Status"`
	Restarts  int               `json:"Restarts"`
	Health    string            `json:"Health"`
	ExitCode  int               `json:"ExitCode"`
	Created   time.Time         `json:"Created"`
	Ports     []string          `json:"Ports"`
	Networks  map[string]string `json:"Networks"`
	Labels    map[string]string `json:"Labels"`
	OneOff    bool              `json:"OneOff"`
	IPAddress string            `json:"IPAddress"`
}

// Rows collects the project's containers as PsRows.
func (r *Runner) Rows(ctx context.Context, o PsOptions) ([]PsRow, error) {
	cs, err := r.containers(ctx, o.All || len(o.Status) > 0)
	if err != nil {
		return nil, err
	}
	cs = serviceContainers(cs, o.Services, true)
	var rows []PsRow
	for i := range cs {
		c := &cs[i]
		svc := c.Label(project.LabelService)
		if !o.Orphans {
			if _, err := r.Project.GetService(svc); err != nil && len(r.Project.Services) > 0 && !o.All {
				// Keep orphans visible: Docker lists them too.
			}
		}
		state := c.Status.State
		if state == "stopped" {
			state = "exited"
		}
		if len(o.Status) > 0 && !containsString(o.Status, state) {
			continue
		}
		row := PsRow{
			Name:      c.ID,
			Service:   svc,
			Image:     displayImage(c.Configuration.Image.Reference),
			Command:   c.Command(),
			Project:   r.Project.Name,
			State:     state,
			Created:   c.Configuration.CreationDate,
			Labels:    c.Configuration.Labels,
			OneOff:    c.Label(project.LabelOneOff) == "True",
			Networks:  map[string]string{},
			IPAddress: c.PrimaryIP(),
		}
		for _, n := range c.Status.Networks {
			row.Networks[n.Network] = n.IP()
		}
		for _, p := range c.Configuration.PublishedPorts {
			addr := p.HostAddress
			if addr == "" {
				addr = "0.0.0.0"
			}
			row.Ports = append(row.Ports, fmt.Sprintf("%s:%d->%d/%s", addr, p.HostPort, p.ContainerPort, p.Proto))
		}
		row.Restarts = restartCount(r.Project.Name, c.ID)
		switch state {
		case "running":
			row.Status = "Up " + humanDuration(time.Since(c.Status.StartedDate))
			if row.Restarts > 0 {
				row.Status += fmt.Sprintf(" (restarted %d)", row.Restarts)
			}
			if o.Health {
				if s, err := r.Project.GetService(svc); err == nil {
					row.Health = string(r.CheckHealth(ctx, c, s))
					if row.Health != "" {
						row.Status += " (" + row.Health + ")"
					}
				}
			}
		case "exited":
			code, ok := r.recordedExit(c.ID)
			if ok {
				row.ExitCode = code
				row.Status = fmt.Sprintf("Exited (%d)", code)
			} else {
				row.Status = "Exited"
			}
		default:
			row.Status = strings.Title(state)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Ps prints the project's containers.
func (r *Runner) Ps(ctx context.Context, o PsOptions) error {
	rows, err := r.Rows(ctx, o)
	if err != nil {
		return err
	}
	out := r.Console.Out
	switch {
	case o.Quiet:
		for _, row := range rows {
			fmt.Fprintln(out, row.Name)
		}
	case o.ServicesOnly:
		seen := map[string]bool{}
		var names []string
		for _, row := range rows {
			if !seen[row.Service] {
				seen[row.Service] = true
				names = append(names, row.Service)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintln(out, n)
		}
	case o.Format == "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if rows == nil {
			rows = []PsRow{}
		}
		return enc.Encode(rows)
	default:
		var table [][]string
		for _, row := range rows {
			table = append(table, []string{row.Name, row.Image, quoteCommand(row.Command), row.Service, humanDuration(time.Since(row.Created)) + " ago", row.Status, strings.Join(row.Ports, ", ")})
		}
		ui.Table(out, []string{"NAME", "IMAGE", "COMMAND", "SERVICE", "CREATED", "STATUS", "PORTS"}, table)
	}
	return nil
}

func displayImage(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "docker.io/library/"), "docker.io/")
}

func quoteCommand(cmd string) string {
	if len(cmd) > 40 {
		cmd = cmd[:37] + "…"
	}
	return `"` + cmd + `"`
}

// humanDuration renders an age the way Docker does.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "Less than a second"
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
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

// findContainer returns the numbered replica of a service.
func (r *Runner) findContainer(ctx context.Context, service string, index int, all bool) (*engine.Container, error) {
	cs, err := r.containers(ctx, all)
	if err != nil {
		return nil, err
	}
	cs = serviceContainers(cs, []string{service}, false)
	if len(cs) == 0 {
		return nil, fmt.Errorf("service %q has no %scontainer", service, map[bool]string{true: "", false: "running "}[all])
	}
	if index <= 0 {
		return &cs[0], nil
	}
	for i := range cs {
		if containerNumber(cs[i]) == index {
			return &cs[i], nil
		}
	}
	return nil, fmt.Errorf("service %q has no container with index %d", service, index)
}
