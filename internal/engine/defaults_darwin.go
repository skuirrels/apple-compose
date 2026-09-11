package engine

import (
	"runtime"

	"golang.org/x/sys/unix"
)

func hostMemoryBytes() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}

func hostCPUs() int { return runtime.NumCPU() }
