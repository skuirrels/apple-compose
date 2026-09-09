// Package ui renders apple-compose's terminal output: progress lines, tables,
// and prefixed, coloured service logs in the style of Docker Compose.
package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
)

// ANSI colour codes used for service prefixes, in Docker Compose's order.
var prefixColours = []string{"36", "33", "32", "35", "34", "36;1", "33;1", "32;1", "35;1", "34;1"}

const (
	green = "32"
	red   = "31"
	grey  = "90"
	bold  = "1"
)

// Console writes status output and knows whether colour is wanted.
type Console struct {
	Out    io.Writer
	Err    io.Writer
	Colour bool
	Quiet  bool
	mu     sync.Mutex
}

// NewConsole builds a console honouring --ansi and the terminal.
func NewConsole(ansi string) *Console {
	c := &Console{Out: os.Stdout, Err: os.Stderr}
	switch ansi {
	case "always":
		c.Colour = true
	case "never":
		c.Colour = false
	default:
		c.Colour = IsTerminal(os.Stderr) && os.Getenv("NO_COLOR") == ""
	}
	return c
}

// IsTerminal reports whether w is a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Paint wraps s in an ANSI colour when colour is enabled.
func (c *Console) Paint(code, s string) string {
	if !c.Colour || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Step prints a completed action, e.g. " ✔ Container app-db-1  Started".
func (c *Console) Step(kind, name, action string) {
	if c.Quiet {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.Err, " %s %s %s  %s\n", c.Paint(green, "✔"), kind, c.Paint(bold, name), c.Paint(green, action))
}

// Fail prints a failed action.
func (c *Console) Fail(kind, name, action string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.Err, " %s %s %s  %s: %v\n", c.Paint(red, "✘"), kind, c.Paint(bold, name), c.Paint(red, action), err)
}

// Info prints a neutral status line.
func (c *Console) Info(format string, args ...any) {
	if c.Quiet {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.Err, format+"\n", args...)
}

// Warn prints a warning line.
func (c *Console) Warn(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.Err, "%s %s\n", c.Paint("33", "warning:"), fmt.Sprintf(format, args...))
}

// HumanDuration renders an age the way Docker does.
func HumanDuration(d time.Duration) string {
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

// DisplayImage strips the docker.io prefixes Docker hides.
func DisplayImage(ref string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ref, "docker.io/library/"), "docker.io/")
}

// SplitRef separates an image reference into repository and tag or digest.
func SplitRef(ref string) (string, string) {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// Capitalise upper-cases the first letter of s.
func Capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Table prints rows aligned under headers.
func Table(w io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 8, 3, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// Prefixer is a line-buffered writer that prepends a coloured service prefix
// to every line, so interleaved output from many containers stays readable.
type Prefixer struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
	buf    bytes.Buffer
	lock   *sync.Mutex
}

// PrefixSet assigns aligned, coloured prefixes to a set of names.
type PrefixSet struct {
	console  *Console
	width    int
	colours  map[string]string
	out      io.Writer
	mu       sync.Mutex
	NoPrefix bool
}

// NewPrefixSet prepares prefixes for names, padding them to equal width.
func NewPrefixSet(console *Console, out io.Writer, names []string, noPrefix bool) *PrefixSet {
	ps := &PrefixSet{console: console, out: out, colours: map[string]string{}, NoPrefix: noPrefix}
	for i, n := range names {
		if len(n) > ps.width {
			ps.width = len(n)
		}
		ps.colours[n] = prefixColours[i%len(prefixColours)]
	}
	return ps
}

// Writer returns a writer prefixing lines with name.
func (ps *PrefixSet) Writer(name string) io.Writer {
	if ps.NoPrefix {
		return &Prefixer{w: ps.out, lock: &ps.mu}
	}
	colour, ok := ps.colours[name]
	if !ok {
		colour = prefixColours[len(ps.colours)%len(prefixColours)]
		ps.colours[name] = colour
		if len(name) > ps.width {
			ps.width = len(name)
		}
	}
	padded := name + strings.Repeat(" ", ps.width-len(name))
	return &Prefixer{w: ps.out, prefix: ps.console.Paint(colour, padded+" |") + " ", lock: &ps.mu}
}

// Write buffers partial lines and emits complete ones with the prefix.
func (p *Prefixer) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf.Write(b)
	for {
		line, err := p.buf.ReadBytes('\n')
		if err != nil {
			p.buf.Write(line)
			break
		}
		p.emit(line)
	}
	return len(b), nil
}

// Flush writes any trailing partial line.
func (p *Prefixer) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.buf.Len() > 0 {
		line := append(p.buf.Bytes(), '\n')
		p.buf.Reset()
		p.emit(line)
	}
}

func (p *Prefixer) emit(line []byte) {
	p.lock.Lock()
	defer p.lock.Unlock()
	io.WriteString(p.w, p.prefix)
	p.w.Write(line)
}
