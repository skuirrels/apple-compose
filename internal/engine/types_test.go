package engine

import (
	"encoding/json"
	"testing"
)

const sampleContainer = `[{"configuration":{"capAdd":[],"creationDate":"2026-09-08T23:15:46Z","dns":{"nameservers":[],"options":[],"searchDomains":["proj"]},"id":"e1","image":{"descriptor":{"digest":"sha256:d9e8","mediaType":"application/vnd.oci.image.index.v1+json","size":9226},"reference":"docker.io/library/alpine:3.20"},"initProcess":{"arguments":["600"],"environment":["PATH=/bin"],"executable":"sleep","rlimits":[],"supplementalGroups":[],"terminal":false,"user":{"id":{"gid":0,"uid":0}},"workingDirectory":"/"},"labels":{"com.docker.compose.project":"demo"},"mounts":[],"networks":[{"network":"default","options":{"hostname":"e1","mtu":1280}}],"platform":{"architecture":"arm64","os":"linux"},"publishedPorts":[{"containerPort":8000,"count":1,"hostAddress":"127.0.0.1","hostPort":18080,"proto":"tcp"}],"publishedSockets":[],"readOnly":false,"resources":{"cpuOverhead":1,"cpus":4,"memoryInBytes":1073741824},"rosetta":false,"runtimeHandler":"container-runtime-linux","ssh":false,"sysctls":{},"useInit":false,"virtualization":false},"id":"e1","status":{"networks":[{"hostname":"e1","ipv4Address":"192.168.65.4/24","ipv4Gateway":"192.168.65.1","ipv6Address":"fdfb::1/64","macAddress":"f2:f4:d2:dd:f4:fb","mtu":1280,"network":"default","variant":"reserved"}],"startedDate":"2026-09-08T23:15:48Z","state":"running"}}]`

func TestDecodeContainer(t *testing.T) {
	var cs []Container
	if err := json.Unmarshal([]byte(sampleContainer), &cs); err != nil {
		t.Fatal(err)
	}
	c := &cs[0]
	if c.ID != "e1" || !c.Running() || c.PrimaryIP() != "192.168.65.4" || c.Gateway() != "192.168.65.1" {
		t.Fatalf("unexpected decode: %+v", c)
	}
	if c.Label(LabelProject) != "demo" || c.Command() != "sleep 600" {
		t.Fatalf("labels/command: %q %q", c.Label(LabelProject), c.Command())
	}
	if p := c.Configuration.PublishedPorts[0]; p.HostPort != 18080 || p.ContainerPort != 8000 || p.HostAddress != "127.0.0.1" {
		t.Fatalf("ports: %+v", p)
	}
	if c.Attachment("default") == nil || c.Attachment("other") != nil {
		t.Fatal("attachment lookup")
	}
}

func TestExitErrorMessage(t *testing.T) {
	e := &ExitError{Args: []string{"network", "create", "--label", "x", "foo"}, Code: 1, Stderr: "Error: network foo exists\n"}
	if e.Error() != "network foo exists" {
		t.Fatalf("got %q", e.Error())
	}
	e = &ExitError{Args: []string{"stop", "x"}, Code: 2}
	if e.Error() != "`container stop x` exited with status 2" {
		t.Fatalf("got %q", e.Error())
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"run", "-e", "A=b c", "it's"})
	if got != `run -e 'A=b c' 'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}
