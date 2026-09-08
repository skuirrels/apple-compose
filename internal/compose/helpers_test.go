package compose

import "github.com/skuirrels/apple-compose/internal/engine"

type engineContainer struct {
	id     string
	labels map[string]string
}

func toContainers(in []engineContainer) []engine.Container {
	out := make([]engine.Container, len(in))
	for i, c := range in {
		out[i].ID = c.id
		out[i].Configuration.Labels = c.labels
		out[i].Status.State = "running"
	}
	return out
}
