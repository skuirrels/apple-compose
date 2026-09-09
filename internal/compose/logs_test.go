package compose

import (
	"bytes"
	"testing"
	"time"

	"github.com/skuirrels/apple-compose/internal/engine"
)

func TestParseTime(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	if got, err := ParseTime("30m", now); err != nil || !got.Equal(now.Add(-30*time.Minute)) {
		t.Errorf("duration: %v %v", got, err)
	}
	if got, err := ParseTime("2026-09-09T10:00:00Z", now); err != nil || got.Hour() != 10 {
		t.Errorf("rfc3339: %v %v", got, err)
	}
	if got, err := ParseTime("1757419200", now); err != nil || got.Unix() != 1757419200 {
		t.Errorf("unix: %v %v", got, err)
	}
	if got, err := ParseTime("2026-09-09", now); err != nil || got.Day() != 9 {
		t.Errorf("date: %v %v", got, err)
	}
	if got, err := ParseTime("", now); err != nil || !got.IsZero() {
		t.Errorf("empty must be zero: %v %v", got, err)
	}
	if _, err := ParseTime("yesterday-ish", now); err == nil {
		t.Error("junk must error")
	}
}

func TestTimedWriterStampsAndFilters(t *testing.T) {
	out := &bytes.Buffer{}
	clock := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	w := &timedWriter{w: out, stamp: true, now: func() time.Time { return clock }}
	w.Write([]byte("one\ntw"))
	w.Write([]byte("o\n"))
	if out.String() != "2026-09-09T12:00:00Z one\n2026-09-09T12:00:00Z two\n" {
		t.Fatalf("stamped output = %q", out.String())
	}
	out.Reset()
	w = &timedWriter{w: out, now: func() time.Time { return clock }, until: clock.Add(-time.Minute)}
	w.Write([]byte("late\n"))
	if out.Len() != 0 {
		t.Fatalf("lines after --until must be dropped: %q", out.String())
	}
	w = &timedWriter{w: out, now: func() time.Time { return clock }, since: clock.Add(-time.Minute)}
	w.Write([]byte("kept"))
	w.Flush()
	if out.String() != "kept\n" {
		t.Fatalf("flush must emit within window: %q", out.String())
	}
}

func TestInWindow(t *testing.T) {
	c := engine.Container{}
	c.Status.StartedDate = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	if !inWindow(c, time.Time{}, time.Time{}) {
		t.Error("no window includes everything")
	}
	if inWindow(c, c.Status.StartedDate.Add(time.Hour), time.Time{}) {
		t.Error("container started before --since must be excluded")
	}
	if !inWindow(c, c.Status.StartedDate.Add(-time.Hour), c.Status.StartedDate.Add(time.Hour)) {
		t.Error("container inside window must be included")
	}
	if inWindow(c, time.Time{}, c.Status.StartedDate.Add(-time.Hour)) {
		t.Error("container started after --until must be excluded")
	}
}
