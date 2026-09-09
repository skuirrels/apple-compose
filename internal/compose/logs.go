package compose

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
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
	// Timestamps prefixes each line with the time apple-compose read it.
	// The runtime stores raw output, so stored lines carry the read time.
	Timestamps bool
	// Since and Until bound output by time. Stored output has no per-line
	// times, so a container's stored log is included when the container
	// started inside the window; live output is filtered line by line.
	Since, Until time.Time
}

// ParseTime accepts Docker's --since/--until forms: an RFC3339 time, a
// date, a Unix timestamp, or a relative duration such as 10m or 1h30m.
func ParseTime(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(secs, 0), nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q: use RFC3339, YYYY-MM-DD, a Unix timestamp, or a duration like 10m", s)
}

// timedWriter stamps and filters lines by the time they are read.
type timedWriter struct {
	w     io.Writer
	buf   bytes.Buffer
	stamp bool
	since time.Time
	until time.Time
	now   func() time.Time
	mu    sync.Mutex
}

func (t *timedWriter) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(b)
	for {
		line, err := t.buf.ReadBytes('\n')
		if err != nil {
			t.buf.Write(line)
			break
		}
		t.emit(line)
	}
	return len(b), nil
}

func (t *timedWriter) emit(line []byte) {
	at := t.now()
	if !t.since.IsZero() && at.Before(t.since) {
		return
	}
	if !t.until.IsZero() && at.After(t.until) {
		return
	}
	if t.stamp {
		io.WriteString(t.w, at.Format(time.RFC3339Nano)+" ")
	}
	t.w.Write(line)
}

// Flush writes a trailing partial line.
func (t *timedWriter) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf.Len() > 0 {
		line := append(t.buf.Bytes(), '\n')
		t.buf.Reset()
		t.emit(line)
	}
	if p, ok := t.w.(interface{ Flush() }); ok {
		p.Flush()
	}
}

// inWindow reports whether a container's stored log falls inside the
// requested window, judged by its start time.
func inWindow(c engine.Container, since, until time.Time) bool {
	started := c.Status.StartedDate
	if !since.IsZero() && !started.IsZero() && started.Before(since) {
		return false
	}
	if !until.IsZero() && !started.IsZero() && started.After(until) {
		return false
	}
	return true
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
	if !o.Since.IsZero() || !o.Until.IsZero() {
		r.warnOnce("logs-window", "the container runtime stores log lines without timestamps; --since/--until include a container's stored output only when it started inside the window")
	}
	var wg sync.WaitGroup
	for _, c := range cs {
		c := c
		if !o.Follow && !c.Running() && c.Status.StartedDate.IsZero() {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var w io.Writer = prefixes.Writer(c.ID)
			w = &timedWriter{w: w, stamp: o.Timestamps, now: time.Now, until: o.Until}
			follow := o.Follow && c.Running()
			if inWindow(c, o.Since, o.Until) {
				cmd := r.Engine.LogsCommand(lctx, c.ID, follow, o.Tail)
				cmd.Stdout = w
				cmd.Stderr = w
				engine.Detach(cmd)
				_ = cmd.Run()
			} else if follow {
				// Stored lines predate the window; stream only new output.
				cmd := r.Engine.LogsCommand(lctx, c.ID, true, 0)
				cmd.Stdout = w
				cmd.Stderr = w
				engine.Detach(cmd)
				_ = cmd.Run()
			}
			if p, ok := w.(interface{ Flush() }); ok {
				p.Flush()
			}
		}()
	}
	if !o.Until.IsZero() && o.Follow {
		go func() {
			select {
			case <-lctx.Done():
			case <-time.After(time.Until(o.Until)):
				cancel()
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
