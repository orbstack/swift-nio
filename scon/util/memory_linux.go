//go:build linux

package util

import "golang.org/x/sys/unix"

func PhysicalMemory() (uint64, error) {
	var sysinfo unix.Sysinfo_t
	if err := unix.Sysinfo(&sysinfo); err != nil {
		return 0, err
	}

	return uint64(sysinfo.Totalram) * uint64(sysinfo.Unit), nil
}
