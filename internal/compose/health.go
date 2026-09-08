package compose

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/engine"
)

// Health is the observed health of a container.
type Health string

// Health states, named as Docker reports them.
const (
	HealthNone      Health = ""
	HealthStarting  Health = "starting"
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
)

// healthSpec is a healthcheck with defaults applied.
type healthSpec struct {
	cmd           []string
	interval      time.Duration
	timeout       time.Duration
	retries       int
	startPeriod   time.Duration
	startInterval time.Duration
}

// healthcheckFor resolves the effective healthcheck of a service. The runtime
// has no built-in healthchecks, so apple-compose probes the container itself
// with `container exec` using the compose semantics.
func healthcheckFor(s types.ServiceConfig) *healthSpec {
	hc := s.HealthCheck
	if hc == nil || hc.Disable || len(hc.Test) == 0 {
		return nil
	}
	var cmd []string
	switch strings.ToUpper(hc.Test[0]) {
	case "NONE":
		return nil
	case "CMD":
		cmd = hc.Test[1:]
	case "CMD-SHELL":
		cmd = []string{"/bin/sh", "-c", strings.Join(hc.Test[1:], " ")}
	default:
		cmd = []string{"/bin/sh", "-c", strings.Join(hc.Test, " ")}
	}
	if len(cmd) == 0 {
		return nil
	}
	spec := &healthSpec{cmd: cmd, interval: 30 * time.Second, timeout: 30 * time.Second, retries: 3, startInterval: 5 * time.Second}
	if hc.Interval != nil {
		spec.interval = time.Duration(*hc.Interval)
	}
	if hc.Timeout != nil {
		spec.timeout = time.Duration(*hc.Timeout)
	}
	if hc.Retries != nil {
		spec.retries = int(*hc.Retries)
	}
	if hc.StartPeriod != nil {
		spec.startPeriod = time.Duration(*hc.StartPeriod)
	}
	if hc.StartInterval != nil {
		spec.startInterval = time.Duration(*hc.StartInterval)
	}
	return spec
}

// probe runs the healthcheck command once.
func (r *Runner) probe(ctx context.Context, id string, spec *healthSpec) bool {
	pctx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()
	code, err := r.Engine.Exec(pctx, id, engine.ExecOptions{}, spec.cmd, nil, discard, discard)
	return err == nil && code == 0
}

// waitHealthy blocks until the container passes its healthcheck, or returns an
// error once it has failed `retries` consecutive probes after the start period.
// The first probe runs immediately so a service that is already healthy does
// not wait a full interval.
func (r *Runner) waitHealthy(ctx context.Context, c *engine.Container, spec *healthSpec, started time.Time) error {
	failures := 0
	for {
		if r.probe(ctx, c.ID, spec) {
			return nil
		}
		inStart := time.Since(started) < spec.startPeriod
		if !inStart {
			failures++
			if failures >= spec.retries {
				return fmt.Errorf("container %s is unhealthy", c.ID)
			}
		}
		cur, err := r.Engine.InspectContainer(ctx, c.ID)
		if err != nil {
			return err
		}
		if !cur.Running() {
			return fmt.Errorf("container %s exited before becoming healthy", c.ID)
		}
		wait := spec.interval
		if inStart && spec.startInterval > 0 {
			wait = spec.startInterval
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// CheckHealth probes a running container once and reports its health, or
// HealthNone when the service has no healthcheck.
func (r *Runner) CheckHealth(ctx context.Context, c *engine.Container, s types.ServiceConfig) Health {
	spec := healthcheckFor(s)
	if spec == nil || !c.Running() {
		return HealthNone
	}
	if r.probe(ctx, c.ID, spec) {
		return HealthHealthy
	}
	if time.Since(c.Status.StartedDate) < spec.startPeriod {
		return HealthStarting
	}
	return HealthUnhealthy
}

// PlainExec returns exec options for a non-interactive command.
func PlainExec() engine.ExecOptions { return engine.ExecOptions{} }
