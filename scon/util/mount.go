package util

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// faster and simpler, but can't detect bind mounts
func IsMountpointSimple(path string) bool {
	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if err != nil {
		return false
	}

	var parentStat unix.Stat_t
	err = unix.Stat(path+"/..", &parentStat)
	if err != nil {
		return false
	}

	return stat.Dev != parentStat.Dev
}

// slower and more complex, but can detect bind mounts
func IsMountpointFull(path string) (bool, error) {
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(string(mountinfo), "\n") {
		parts := strings.Split(line, " ")
		if len(parts) < 5 {
			continue
		}

		if parts[4] == path {
			return true, nil
		}
	}

	return false, nil
}
