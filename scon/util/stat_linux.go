package util

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func GetDiskSizeBytes(path string) (uint64, error) {
	var statfs unix.Statfs_t
	err := unix.Statfs(path, &statfs)
	if err != nil {
		return 0, fmt.Errorf("statfs: %w", err)
	}

	return statfs.Blocks * uint64(statfs.Bsize), nil
}
