package engine

import (
	"strings"
	"time"
)

// Container mirrors one element of `container ls --format json`.
type Container struct {
	ID            string                 `json:"id"`
	Configuration ContainerConfiguration `json:"configuration"`
	Status        ContainerStatus        `json:"status"`
}

// ContainerConfiguration is the immutable part of a container record.
type ContainerConfiguration struct {
	ID             string            `json:"id"`
	CreationDate   time.Time         `json:"creationDate"`
	Labels         map[string]string `json:"labels"`
	Image          ImageReference    `json:"image"`
	InitProcess    InitProcess       `json:"initProcess"`
	Networks       []NetworkConfig   `json:"networks"`
	PublishedPorts []PublishedPort   `json:"publishedPorts"`
	Mounts         []map[string]any  `json:"mounts"`
	Platform       Platform          `json:"platform"`
	Resources      Resources         `json:"resources"`
	DNS            *DNSConfig        `json:"dns"`
	ReadOnly       bool              `json:"readOnly"`
	UseInit        bool              `json:"useInit"`
	Rosetta        bool              `json:"rosetta"`
	Sysctls        map[string]string `json:"sysctls"`
	Extra          map[string]any    `json:"-"`
}

// ImageReference names the image a container was created from.
type ImageReference struct {
	Reference  string `json:"reference"`
	Descriptor struct {
		Digest    string `json:"digest"`
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
	} `json:"descriptor"`
}

// InitProcess is the container's PID 1 specification.
type InitProcess struct {
	Executable       string   `json:"executable"`
	Arguments        []string `json:"arguments"`
	Environment      []string `json:"environment"`
	WorkingDirectory string   `json:"workingDirectory"`
	Terminal         bool     `json:"terminal"`
}

// NetworkConfig is a requested network attachment.
type NetworkConfig struct {
	Network string         `json:"network"`
	Options map[string]any `json:"options"`
}

// PublishedPort is a host to container port forward.
type PublishedPort struct {
	ContainerPort int    `json:"containerPort"`
	HostAddress   string `json:"hostAddress"`
	HostPort      int    `json:"hostPort"`
	Proto         string `json:"proto"`
	Count         int    `json:"count"`
}

// Platform is an OCI platform.
type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

// Resources are the VM resources assigned to a container.
type Resources struct {
	CPUs          int   `json:"cpus"`
	MemoryInBytes int64 `json:"memoryInBytes"`
}

// DNSConfig is the resolver configuration written into the container.
type DNSConfig struct {
	Nameservers   []string `json:"nameservers"`
	Domain        string   `json:"domain"`
	SearchDomains []string `json:"searchDomains"`
	Options       []string `json:"options"`
}

// ContainerStatus is the mutable runtime state.
type ContainerStatus struct {
	State       string              `json:"state"`
	StartedDate time.Time           `json:"startedDate"`
	Networks    []NetworkAttachment `json:"networks"`
}

// NetworkAttachment is a live address on a network.
type NetworkAttachment struct {
	Network     string `json:"network"`
	Hostname    string `json:"hostname"`
	IPv4Address string `json:"ipv4Address"`
	IPv4Gateway string `json:"ipv4Gateway"`
	IPv6Address string `json:"ipv6Address"`
	MacAddress  string `json:"macAddress"`
}

// IP returns the address without its prefix length.
func (n NetworkAttachment) IP() string {
	ip, _, _ := strings.Cut(n.IPv4Address, "/")
	return ip
}

// Label returns a label value or "".
func (c *Container) Label(key string) string {
	if c == nil {
		return ""
	}
	return c.Configuration.Labels[key]
}

// Running reports whether the container is in the running state.
func (c *Container) Running() bool { return c != nil && c.Status.State == "running" }

// Attachment returns the live attachment on the named network, if any.
func (c *Container) Attachment(network string) *NetworkAttachment {
	for i := range c.Status.Networks {
		if c.Status.Networks[i].Network == network {
			return &c.Status.Networks[i]
		}
	}
	return nil
}

// PrimaryIP is the address on the first network, or "" when stopped.
func (c *Container) PrimaryIP() string {
	if len(c.Status.Networks) == 0 {
		return ""
	}
	return c.Status.Networks[0].IP()
}

// Gateway is the gateway of the first network, which is the macOS host.
func (c *Container) Gateway() string {
	if len(c.Status.Networks) == 0 {
		return ""
	}
	return c.Status.Networks[0].IPv4Gateway
}

// Command renders the init process as a single string.
func (c *Container) Command() string {
	parts := append([]string{c.Configuration.InitProcess.Executable}, c.Configuration.InitProcess.Arguments...)
	return strings.Join(parts, " ")
}

// Network mirrors one element of `container network ls --format json`.
type Network struct {
	ID            string `json:"id"`
	Configuration struct {
		Name         string            `json:"name"`
		Mode         string            `json:"mode"`
		Labels       map[string]string `json:"labels"`
		CreationDate time.Time         `json:"creationDate"`
	} `json:"configuration"`
	Status struct {
		IPv4Gateway string `json:"ipv4Gateway"`
		IPv4Subnet  string `json:"ipv4Subnet"`
		IPv6Subnet  string `json:"ipv6Subnet"`
	} `json:"status"`
}

// Label returns a network label or "".
func (n *Network) Label(key string) string {
	if n == nil {
		return ""
	}
	return n.Configuration.Labels[key]
}

// Volume mirrors one element of `container volume ls --format json`.
type Volume struct {
	ID            string `json:"id"`
	Configuration struct {
		Name         string            `json:"name"`
		Driver       string            `json:"driver"`
		Format       string            `json:"format"`
		Labels       map[string]string `json:"labels"`
		SizeInBytes  int64             `json:"sizeInBytes"`
		Source       string            `json:"source"`
		CreationDate time.Time         `json:"creationDate"`
	} `json:"configuration"`
}

// Label returns a volume label or "".
func (v *Volume) Label(key string) string {
	if v == nil {
		return ""
	}
	return v.Configuration.Labels[key]
}

// Image mirrors one element of `container image ls --format json`.
type Image struct {
	ID            string `json:"id"`
	Configuration struct {
		Name         string    `json:"name"`
		CreationDate time.Time `json:"creationDate"`
		Descriptor   struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"descriptor"`
	} `json:"configuration"`
	Variants []ImageVariant `json:"variants"`
}

// ImageVariant is one platform-specific manifest of an image.
type ImageVariant struct {
	Platform Platform `json:"platform"`
	Size     int64    `json:"size"`
	Config   struct {
		Config struct {
			Cmd          []string       `json:"Cmd"`
			Entrypoint   []string       `json:"Entrypoint"`
			Env          []string       `json:"Env"`
			WorkingDir   string         `json:"WorkingDir"`
			User         string         `json:"User"`
			StopSignal   string         `json:"StopSignal"`
			ExposedPorts map[string]any `json:"ExposedPorts"`
		} `json:"config"`
	} `json:"config"`
}

// Name is the normalised reference, e.g. docker.io/library/alpine:3.20.
func (i *Image) Name() string { return i.Configuration.Name }
