// Package enginetest provides a fake `container` executable for tests: a
// shell script that records every invocation and answers registered
// prefixes with canned output, so orchestration logic can be exercised
// without booting virtual machines.
package enginetest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/engine"
)

type response struct {
	prefix string
	stdout string
	code   int
	// seq holds successive outputs for repeated calls; the last one repeats.
	seq []string
}

// Fake is a scripted stand-in for the container CLI.
type Fake struct {
	t         testing.TB
	dir       string
	script    string
	log       string
	responses []response
	// Engine is wired to the fake script.
	Engine *engine.Engine
}

// New creates the fake and an Engine pointing at it. Every call succeeds
// with empty output unless a response is registered with On.
func New(t testing.TB) *Fake {
	t.Helper()
	dir := t.TempDir()
	f := &Fake{t: t, dir: dir, script: filepath.Join(dir, "container"), log: filepath.Join(dir, "calls.log")}
	f.write()
	f.Engine = &engine.Engine{Bin: f.script, Log: os.Stderr}
	return f
}

// On answers invocations whose argument string starts with prefix. The
// longest matching prefix wins; among equal prefixes the latest registration
// does.
func (f *Fake) On(prefix, stdout string, code int) {
	f.responses = append([]response{{prefix: prefix, stdout: stdout, code: code}}, f.responses...)
	f.write()
}

// OnSequence answers successive invocations of prefix with successive
// outputs, repeating the last one once they run out.
func (f *Fake) OnSequence(prefix string, outputs ...string) {
	f.responses = append([]response{{prefix: prefix, seq: outputs}}, f.responses...)
	f.write()
}

// Calls returns the recorded invocations, one argument string per call.
func (f *Fake) Calls() []string {
	b, err := os.ReadFile(f.log)
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// Reset forgets recorded calls.
func (f *Fake) Reset() { _ = os.Remove(f.log) }

// Called reports whether any recorded call starts with prefix.
func (f *Fake) Called(prefix string) bool {
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// Call returns the first recorded call starting with prefix, or "".
func (f *Fake) Call(prefix string) string {
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	return ""
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (f *Fake) write() {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "printf '%%s\\n' \"$*\" >> %s\n", shellQuote(f.log))
	ordered := append([]response(nil), f.responses...)
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i].prefix) > len(ordered[j].prefix) })
	b.WriteString("case \"$*\" in\n")
	for i, r := range ordered {
		if r.seq != nil {
			counter := filepath.Join(f.dir, fmt.Sprintf("seq_%d", i))
			fmt.Fprintf(&b, "  %s*) n=$(cat %s 2>/dev/null || echo 0); echo $((n+1)) > %s; case $n in\n", shellQuote(r.prefix), shellQuote(counter), shellQuote(counter))
			for j, out := range r.seq {
				sel := fmt.Sprintf("%d", j)
				if j == len(r.seq)-1 {
					sel = "*"
				}
				fmt.Fprintf(&b, "    %s) printf '%%s' %s;;\n", sel, shellQuote(out))
			}
			b.WriteString("  esac; exit 0;;\n")
			continue
		}
		// The real CLI reports failures on stderr, so canned output for a
		// non-zero exit goes there too.
		redirect := ""
		if r.code != 0 {
			redirect = " >&2"
		}
		fmt.Fprintf(&b, "  %s*) printf '%%s' %s%s; exit %d;;\n", shellQuote(r.prefix), shellQuote(r.stdout), redirect, r.code)
	}
	b.WriteString("esac\nexit 0\n")
	if err := os.WriteFile(f.script, []byte(b.String()), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// ContainerJSON renders a minimal container record as the runtime would.
func ContainerJSON(id, service, project, state, ip, network string, labels map[string]string) string {
	all := map[string]string{
		"com.docker.compose.project":          project,
		"com.docker.compose.service":          service,
		"com.docker.compose.container-number": "1",
		"com.docker.compose.oneoff":           "False",
	}
	for k, v := range labels {
		all[k] = v
	}
	var lb []string
	for k, v := range all {
		lb = append(lb, fmt.Sprintf("%q:%q", k, v))
	}
	status := fmt.Sprintf(`{"state":%q,"startedDate":"2026-09-09T00:00:00Z","networks":[]}`, state)
	if state == "running" {
		status = fmt.Sprintf(`{"state":"running","startedDate":"2026-09-09T00:00:00Z","networks":[{"network":%q,"hostname":%q,"ipv4Address":"%s/24","ipv4Gateway":"192.168.66.1"}]}`, network, id, ip)
	}
	return fmt.Sprintf(`{"id":%q,"configuration":{"id":%q,"creationDate":"2026-09-09T00:00:00Z","labels":{%s},"image":{"reference":"docker.io/library/alpine:3.20"},"initProcess":{"executable":"sleep","arguments":["1"]},"networks":[{"network":%q,"options":{}}],"publishedPorts":[{"containerPort":80,"hostAddress":"0.0.0.0","hostPort":8080,"proto":"tcp","count":1}]},"status":%s}`,
		id, id, strings.Join(lb, ","), network, status)
}
