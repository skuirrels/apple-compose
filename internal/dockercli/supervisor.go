package dockercli

import (
	"context"
	"os"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/skuirrels/apple-compose/internal/compose"
	"github.com/skuirrels/apple-compose/internal/project"
)

// supervisedProject is the pseudo compose project that groups containers
// created by `docker run --restart`, so apple-compose's restart supervisor
// can look after them.
const supervisedProject = compose.DockerProject

// restartLabels tags a container for the supervisor.
func restartLabels(policy, service string) []string {
	return []string{
		"--label", compose.LabelRestart + "=" + policy,
		"--label", project.LabelProject + "=" + supervisedProject,
		"--label", project.LabelService + "=" + service,
		"--label", project.LabelOneOff + "=False",
	}
}

// supervisorRunner returns a compose Runner scoped to the pseudo project.
func (a *App) supervisorRunner() *compose.Runner {
	p := &types.Project{Name: supervisedProject, Services: types.Services{}}
	r := compose.New(a.eng, p, a.console, a.version)
	r.SpawnSupervisor = a.spawnFn
	if r.SpawnSupervisor == nil {
		r.SpawnSupervisor = func(name string, _ []string, logPath string) (int, error) {
			exe, err := os.Executable()
			if err != nil {
				return 0, err
			}
			// The compose subcommand runs apple-compose in-process, so the
			// supervisor is this very binary.
			return compose.SpawnDetached(exe, []string{"compose", "--project-name", name, "supervise"}, logPath)
		}
	}
	return r
}

// ensureSupervisor starts the restart supervisor when a running container
// carries a restart policy. Failures are reported, not fatal.
func (a *App) ensureSupervisor(ctx context.Context) {
	if err := a.supervisorRunner().EnsureSupervisor(ctx); err != nil {
		a.warn("restart supervisor: %v", err)
	}
}

// markStopped records deliberate stops so the supervisor leaves them alone.
func markStopped(ids ...string) { compose.MarkStopped(supervisedProject, ids) }

// clearStopped lifts stop markers before a start.
func clearStopped(ids ...string) { compose.ClearStopped(supervisedProject, ids) }
