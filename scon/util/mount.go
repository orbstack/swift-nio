package util

import (
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

// if we want to detect bind mounts for some reason, we can use
