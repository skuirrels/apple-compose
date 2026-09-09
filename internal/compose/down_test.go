package compose

import (
	"context"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/enginetest"
)

func TestDownStopsInReverseOrderAndRemovesResources(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  web:
    image: img
    depends_on: [db]
    stop_grace_period: 3s
    stop_signal: SIGINT
  db:
    image: img
volumes:
  data: {}
`, nil)
	f := withFake(t, r)
	all := "[" +
		enginetest.ContainerJSON("t-db-1", "db", "t", "running", "10.0.0.2", "t_default", nil) + "," +
		enginetest.ContainerJSON("t-web-1", "web", "t", "running", "10.0.0.3", "t_default", map[string]string{LabelStopSignal: "SIGINT", LabelStopGrace: "3s"}) + "," +
		enginetest.ContainerJSON("t-old-1", "old", "t", "stopped", "", "t_default", nil) + "," +
		enginetest.ContainerJSON("other-x-1", "x", "other", "running", "10.0.0.9", "other_default", nil) +
		"]"
	// The first listing plans the teardown; the second, after deletion,
	// shows the project empty so networks are removed.
	f.OnSequence("ls --format json --all", all, "["+enginetest.ContainerJSON("other-x-1", "x", "other", "running", "10.0.0.9", "other_default", nil)+"]")
	f.On("network ls --format json", `[{"id":"t_default","configuration":{"name":"t_default","labels":{"com.docker.compose.project":"t","com.docker.compose.network":"default"}}},{"id":"other_default","configuration":{"name":"other_default","labels":{"com.docker.compose.project":"other"}}}]`, 0)
	f.On("volume ls --format json", `[{"id":"t_data","configuration":{"name":"t_data","labels":{"com.docker.compose.project":"t","com.docker.compose.volume":"data"}}},{"id":"keep","configuration":{"name":"keep","labels":{}}}]`, 0)
	// After deletion the project has no containers left.
	if err := r.Down(context.Background(), DownOptions{Volumes: true, RemoveOrphans: true}); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	joined := strings.Join(calls, "\n")
	webStop := strings.Index(joined, "stop --signal SIGINT --time 3 t-web-1")
	dbStop := strings.Index(joined, "stop --time 10 t-db-1")
	if webStop < 0 || dbStop < 0 || webStop > dbStop {
		t.Fatalf("web must stop with its own settings before db:\n%s", joined)
	}
	if !strings.Contains(joined, "delete --force t-web-1 t-db-1 t-old-1") {
		t.Fatalf("orphan of a removed service and project containers must be deleted:\n%s", joined)
	}
	if strings.Contains(joined, "other-x-1") {
		t.Fatalf("other projects must be untouched:\n%s", joined)
	}
	if !strings.Contains(joined, "network delete t_default") || strings.Contains(joined, "network delete other_default") {
		t.Fatalf("only the project network must be removed:\n%s", joined)
	}
	if !strings.Contains(joined, "volume delete t_data") || strings.Contains(joined, "volume delete keep") {
		t.Fatalf("only the project volume must be removed:\n%s", joined)
	}
}

func TestDownWithoutVolumesKeepsThem(t *testing.T) {
	r, _ := loadRunner(t, "name: t\nservices:\n  a:\n    image: img\n", nil)
	f := withFake(t, r)
	f.On("ls --format json --all", "[]", 0)
	f.On("network ls --format json", "[]", 0)
	if err := r.Down(context.Background(), DownOptions{}); err != nil {
		t.Fatal(err)
	}
	if f.Called("volume") {
		t.Fatalf("volumes must not be touched: %v", f.Calls())
	}
}

func TestDownLeavesOrphansUnlessAsked(t *testing.T) {
	r, warnings := loadRunner(t, "name: t\nservices:\n  web:\n    image: img\n", nil)
	f := withFake(t, r)
	f.On("ls --format json --all", "["+
		enginetest.ContainerJSON("t-web-1", "web", "t", "running", "10.0.0.3", "t_default", nil)+","+
		enginetest.ContainerJSON("t-old-1", "old", "t", "stopped", "", "t_default", nil)+
		"]", 0)
	if err := r.Down(context.Background(), DownOptions{}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.Calls(), "\n")
	if !strings.Contains(joined, "delete --force t-web-1\n") || strings.Contains(joined, "t-old-1") {
		t.Fatalf("orphans must be left alone without --remove-orphans:\n%s", joined)
	}
	_ = warnings
}
