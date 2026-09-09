package compose

import (
	"reflect"
	"testing"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
)

func TestServiceOrderAndContainerOrdering(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  web:
    image: img
    depends_on: [api]
  api:
    image: img
    depends_on: [db, cache]
  db:
    image: img
  cache:
    image: img
`, nil)
	order := serviceOrder(r.Project, r.Project.ServiceNames())
	if !reflect.DeepEqual(order, []string{"cache", "db", "api", "web"}) {
		t.Fatalf("order = %v", order)
	}
	mk := func(id, svc string, n string) engine.Container {
		c := engine.Container{ID: id}
		c.Configuration.Labels = map[string]string{project.LabelService: svc, project.LabelContainerNumber: n}
		return c
	}
	cs := []engine.Container{mk("t-web-1", "web", "1"), mk("t-db-2", "db", "2"), mk("t-db-1", "db", "1"), mk("t-orphan-1", "orphan", "1"), mk("t-api-1", "api", "1")}
	got := ids(orderContainers(r.Project, cs, true))
	if !reflect.DeepEqual(got, []string{"t-web-1", "t-api-1", "t-db-1", "t-db-2", "t-orphan-1"}) {
		t.Fatalf("reverse order = %v", got)
	}
	got = ids(orderContainers(r.Project, cs, false))
	if got[0] != "t-db-1" || got[len(got)-1] != "t-orphan-1" {
		t.Fatalf("forward order = %v", got)
	}
	sel := serviceContainers(cs, []string{"db"}, false)
	if len(sel) != 2 || sel[0].ID != "t-db-1" {
		t.Fatalf("serviceContainers = %v", ids(sel))
	}
}

func TestExitCodeNeededAndRecordedExit(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  init:
    image: img
  app:
    image: img
    depends_on:
      init:
        condition: service_completed_successfully
`, nil)
	if !r.exitCodeNeeded("init") || r.exitCodeNeeded("app") {
		t.Fatal("exitCodeNeeded")
	}
	if _, ok := r.recordedExit("t-init-1"); ok {
		t.Fatal("no exit recorded yet")
	}
	r.recordExit("t-init-1", 4)
	if code, ok := r.recordedExit("t-init-1"); !ok || code != 4 {
		t.Fatal("recorded exit not read back")
	}
}
