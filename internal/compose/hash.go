package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/compose-spec/compose-go/v2/types"
)

// ServiceHash fingerprints a service's effective configuration so `up` can
// tell whether an existing container is stale. Anything that changes the
// container's runtime arguments must be part of the hash.
func ServiceHash(s types.ServiceConfig) (string, error) {
	// Labels that only describe the project would make the hash unstable
	// between working directories, so drop them the way Docker Compose does.
	s.CustomLabels = nil
	s.Build = nil
	s.PullPolicy = ""
	s.Scale = nil
	if s.Deploy != nil {
		d := *s.Deploy
		d.Replicas = nil
		s.Deploy = &d
	}
	s.DependsOn = nil
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
