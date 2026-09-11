//go:build !darwin

package engine

import "runtime"

// The runtime exists only on macOS; these keep other builds compiling.
func hostMemoryBytes() uint64 { return 0 }

func hostCPUs() int { return runtime.NumCPU() }
