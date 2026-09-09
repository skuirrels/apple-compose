package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrefixerBuffersLines(t *testing.T) {
	out := &bytes.Buffer{}
	c := &Console{Out: out, Err: out, Colour: false}
	ps := NewPrefixSet(c, out, []string{"web", "database"}, false)
	w := ps.Writer("web").(*Prefixer)
	w.Write([]byte("hel"))
	w.Write([]byte("lo\nwor"))
	if out.String() != "web      | hello\n" {
		t.Fatalf("partial line must be held back: %q", out.String())
	}
	w.Flush()
	if !strings.HasSuffix(out.String(), "web      | wor\n") {
		t.Fatalf("flush must emit the remainder: %q", out.String())
	}
	out.Reset()
	np := NewPrefixSet(c, out, []string{"web"}, true)
	np.Writer("web").Write([]byte("plain\n"))
	if out.String() != "plain\n" {
		t.Fatalf("no-prefix output = %q", out.String())
	}
}

func TestConsolePaintAndSteps(t *testing.T) {
	out := &bytes.Buffer{}
	c := &Console{Out: out, Err: out, Colour: true}
	if c.Paint("32", "x") != "\x1b[32mx\x1b[0m" {
		t.Fatal("paint with colour")
	}
	c.Colour = false
	if c.Paint("32", "x") != "x" {
		t.Fatal("paint without colour")
	}
	c.Step("Container", "a", "Started")
	c.Warn("careful %d", 1)
	c.Fail("Network", "n", "Creating", errString("boom"))
	got := out.String()
	for _, want := range []string{" ✔ Container a  Started", "warning: careful 1", " ✘ Network n  Creating: boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	c.Quiet = true
	out.Reset()
	c.Step("Container", "a", "Started")
	c.Info("hidden")
	if out.Len() != 0 {
		t.Error("quiet console must suppress steps and info")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestTable(t *testing.T) {
	out := &bytes.Buffer{}
	Table(out, []string{"A", "BB"}, [][]string{{"1", "2"}, {"333", "4"}})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "A") || !strings.Contains(lines[0], "BB") {
		t.Fatalf("table output:\n%s", out.String())
	}
	if strings.Index(lines[1], "2") != strings.Index(lines[2], "4") {
		t.Fatalf("columns must align:\n%s", out.String())
	}
}
