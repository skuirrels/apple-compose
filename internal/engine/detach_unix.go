package engine

import (
	"os/exec"
	"syscall"
)

// Detach places the child in its own process group so a Ctrl-C at the
// terminal reaches apple-compose alone.
func Detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
