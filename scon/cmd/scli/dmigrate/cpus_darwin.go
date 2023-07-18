//go:build darwin

package dmigrate

import (
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// P-cpus on Apple Silicon, or total logical CPUs if can't be determined
func getPcpuCount() int {
	str, err := unix.Sysctl("hw.perflevel0.logicalcpu_max")
	if err == nil {
		if value, err := strconv.Atoi(str); err == nil {
			return value
		}
	}

	// fallback
	return runtime.NumCPU()
}
