package compose

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/skuirrels/apple-compose/internal/engine"
	"github.com/skuirrels/apple-compose/internal/project"
)

func fakeContainer(t *testing.T, dir, id, service, state string, nets map[string]string, oneOff bool) engine.Container {
	t.Helper()
	c := engine.Container{ID: id}
	c.Configuration.Labels = map[string]string{
		project.LabelService: service,
		LabelHostsFile:       filepath.Join(dir, id),
	}
	if oneOff {
		c.Configuration.Labels[project.LabelOneOff] = "True"
	}
	c.Status.State = state
	// Attachment order matters: the first network supplies the container's
	// own address, as it does on the runtime, so iterate deterministically.
	names := make([]string, 0, len(nets))
	for n := range nets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ip := nets[n]
		c.Configuration.Networks = append(c.Configuration.Networks, engine.NetworkConfig{Network: n})
		if state == "running" {
			c.Status.Networks = append(c.Status.Networks, engine.NetworkAttachment{Network: n, IPv4Address: ip + "/24", IPv4Gateway: "10.0.0.1"})
		}
	}
	return c
}

func readHosts(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWriteHostsFiles(t *testing.T) {
	r, _ := loadRunner(t, `
name: t
services:
  web:
    image: img
    hostname: www
    networks: [front, back]
    extra_hosts:
      - "gw:host-gateway"
      - "fixed:10.9.9.9"
    links: ["db:database"]
  db:
    image: img
    networks:
      back:
        aliases: [pg]
  lonely:
    image: img
    networks: [other]
networks:
  front: {}
  back: {}
  other: {}
`, nil)
	dir := t.TempDir()
	web := fakeContainer(t, dir, "t-web-1", "web", "running", map[string]string{"t_front": "10.1.0.2", "t_back": "10.2.0.2"}, false)
	db := fakeContainer(t, dir, "t-db-1", "db", "running", map[string]string{"t_back": "10.2.0.3"}, false)
	lonely := fakeContainer(t, dir, "t-lonely-1", "lonely", "running", map[string]string{"t_other": "10.3.0.2"}, false)
	stopped := fakeContainer(t, dir, "t-db-2", "db", "stopped", map[string]string{"t_back": ""}, false)
	oneOff := fakeContainer(t, dir, "t-web-run-abc", "web", "running", map[string]string{"t_front": "10.1.0.9"}, true)
	if err := r.writeHostsFiles([]engine.Container{web, db, lonely, stopped, oneOff}); err != nil {
		t.Fatal(err)
	}

	got := readHosts(t, filepath.Join(dir, "t-web-1"))
	// web's own address comes from its first network, which the runtime
	// and the fixture both take in name order: back before front.
	for _, want := range []string{
		"10.2.0.2\tt-web-1 web www",
		"10.2.0.3\tdb t-db-1 pg database",
		"10.0.0.1\thost.docker.internal gateway.docker.internal host.containers.internal gw",
		"10.9.9.9\tfixed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("web hosts missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "lonely") || strings.Contains(got, "t-web-run-abc") {
		t.Errorf("unreachable peers and one-offs must not appear:\n%s", got)
	}

	got = readHosts(t, filepath.Join(dir, "t-db-1"))
	if !strings.Contains(got, "10.2.0.2\tweb t-web-1 www") || strings.Contains(got, "database") {
		t.Errorf("db hosts wrong (web via back network, no link alias):\n%s", got)
	}

	got = readHosts(t, filepath.Join(dir, "t-db-2"))
	if !strings.Contains(got, "127.0.1.1\tt-db-2") || !strings.Contains(got, "10.2.0.2\tweb") {
		t.Errorf("unstarted container must map itself to loopback and still see peers:\n%s", got)
	}

	got = readHosts(t, filepath.Join(dir, "t-web-run-abc"))
	if strings.Contains(got, "10.1.0.9\tt-web-run-abc web") || !strings.Contains(got, "10.1.0.2\tweb t-web-1") {
		t.Errorf("one-off must not claim the service name:\n%s", got)
	}
}

func TestHostnameOfAndSharedIP(t *testing.T) {
	if hostnameOf("a.b.c") != "a" || hostnameOf("plain") != "plain" {
		t.Fatal("hostnameOf")
	}
	a := engine.Container{}
	a.Configuration.Networks = []engine.NetworkConfig{{Network: "n2"}, {Network: "n1"}}
	b := engine.Container{}
	b.Status.State = "running"
	b.Status.Networks = []engine.NetworkAttachment{{Network: "n1", IPv4Address: "10.1.0.5/24"}, {Network: "n2", IPv4Address: "10.2.0.5/24"}}
	if ip := sharedIP(a, b); ip != "10.2.0.5" {
		t.Fatalf("expected first network of a to win, got %s", ip)
	}
}
