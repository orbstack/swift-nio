//go:build !darwin

package dmigrate

import (
	"runtime"
)

// P-cpus on Apple Silicon, or total logical CPUs if can't be determined
func getPcpuCount() int {
	return runtime.NumCPU()
}
