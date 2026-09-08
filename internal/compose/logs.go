package compose

import (
	"context"
	"sync"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
	"github.com/skuirrels/apple-compose/internal/ui"
)

// LogsOptions configure Logs.
type LogsOptions struct {
	Services    []string
	Follow      bool
	Tail        int // -1 for all
	NoLogPrefix bool
	Index       int
}

// Logs prints or follows container logs with service prefixes.
func (r *Runner) Logs(ctx context.Context, o LogsOptions) error {
	cs, err := r.containers(ctx, true)
	if err != nil {
		return err
	}
	cs = serviceContainers(cs, o.Services, false)
	if o.Index > 0 {
		var filtered []engine.Container
		for _, c := range cs {
			if containerNumber(c) == o.Index {
				filtered = append(filtered, c)
			}
		}
		cs = filtered
	}
	if len(cs) == 0 {
		return nil
	}
	var names []string
	for _, c := range cs {
		names = append(names, c.ID)
	}
	prefixes := ui.NewPrefixSet(r.Console, r.Console.Out, names, o.NoLogPrefix)
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range cs {
		c := c
		if !o.Follow && !c.Running() && c.Status.StartedDate.IsZero() {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := prefixes.Writer(c.ID)
			cmd := r.Engine.LogsCommand(lctx, c.ID, o.Follow && c.Running(), o.Tail)
			cmd.Stdout = w
			cmd.Stderr = w
			engine.Detach(cmd)
			_ = cmd.Run()
			if p, ok := w.(interface{ Flush() }); ok {
				p.Flush()
			}
		}()
	}
	if o.Follow {
		// Stop following once every container has exited, as the runtime's
		// own `logs --follow` keeps waiting forever.
		go func() {
			for {
				select {
				case <-lctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
				cur, err := r.containers(lctx, false)
				if err != nil {
					continue
				}
				running := 0
				for _, c := range cur {
					for _, want := range cs {
						if c.ID == want.ID {
							running++
						}
					}
				}
				if running == 0 {
					cancel()
					return
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

// serviceOf returns the service a container belongs to.
func serviceOf(c engine.Container) string { return c.Label(project.LabelService) }
