package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/skuirrels/apple-compose/internal/enginetest"
	"github.com/skuirrels/apple-compose/internal/ui"
)

func TestRowsAndPsFormats(t *testing.T) {
	r, _ := loadRunner(t, "name: t\nservices:\n  web:\n    image: img\n  job:\n    image: img\n", nil)
	f := withFake(t, r)
	f.On("ls --format json", "["+enginetest.ContainerJSON("t-web-1", "web", "t", "running", "10.0.0.2", "t_default", nil)+"]", 0)
	f.On("ls --format json --all", "["+
		enginetest.ContainerJSON("t-web-1", "web", "t", "running", "10.0.0.2", "t_default", nil)+","+
		enginetest.ContainerJSON("t-job-1", "job", "t", "stopped", "", "t_default", nil)+
		"]", 0)
	r.recordExit("t-job-1", 3)
	ctx := context.Background()

	rows, err := r.Rows(ctx, PsOptions{All: true})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows: %v %v", rows, err)
	}
	if rows[0].Service != "job" || rows[0].State != "exited" || rows[0].ExitCode != 3 || rows[0].Status != "Exited (3)" {
		t.Errorf("job row: %+v", rows[0])
	}
	if rows[1].Service != "web" || rows[1].State != "running" || rows[1].IPAddress != "10.0.0.2" || rows[1].Ports[0] != "0.0.0.0:8080->80/tcp" {
		t.Errorf("web row: %+v", rows[1])
	}
	rows, _ = r.Rows(ctx, PsOptions{Status: []string{"exited"}})
	if len(rows) != 1 || rows[0].Service != "job" {
		t.Errorf("status filter: %+v", rows)
	}

	out := &bytes.Buffer{}
	r.Console = &ui.Console{Out: out, Err: &bytes.Buffer{}}
	if err := r.Ps(ctx, PsOptions{Format: "json"}); err != nil {
		t.Fatal(err)
	}
	var decoded []PsRow
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("json output: %v %s", err, out.String())
	}
	out.Reset()
	if err := r.Ps(ctx, PsOptions{Quiet: true, All: true}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "t-job-1\nt-web-1\n" {
		t.Errorf("quiet output = %q", out.String())
	}
	out.Reset()
	if err := r.Ps(ctx, PsOptions{ServicesOnly: true, All: true}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "job\nweb\n" {
		t.Errorf("services output = %q", out.String())
	}
	out.Reset()
	if err := r.Ps(ctx, PsOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "NAME") || !strings.Contains(out.String(), "0.0.0.0:8080->80/tcp") {
		t.Errorf("table output:\n%s", out.String())
	}
}

func TestHelpers(t *testing.T) {
	cases := map[time.Duration]string{
		500 * time.Millisecond: "Less than a second",
		30 * time.Second:       "30 seconds",
		90 * time.Second:       "About a minute",
		5 * time.Minute:        "5 minutes",
		90 * time.Minute:       "About an hour",
		5 * time.Hour:          "5 hours",
		72 * time.Hour:         "3 days",
	}
	for d, want := range cases {
		if got := ui.HumanDuration(d); got != want {
			t.Errorf("HumanDuration(%s) = %q, want %q", d, got, want)
		}
	}
	if repo, tag := ui.SplitRef("ghcr.io/x/y:1.2"); repo != "ghcr.io/x/y" || tag != "1.2" {
		t.Error("SplitRef tag")
	}
	if repo, tag := ui.SplitRef("localhost:5000/img"); repo != "localhost:5000/img" || tag != "latest" {
		t.Error("SplitRef registry port")
	}
	if humanBytes(4093973) != "3.9MB" || humanBytes(512) != "512B" {
		t.Error("humanBytes")
	}
	if ui.DisplayImage("docker.io/library/alpine:3.20") != "alpine:3.20" || ui.DisplayImage("docker.io/foo/bar") != "foo/bar" {
		t.Error("displayImage")
	}
}
