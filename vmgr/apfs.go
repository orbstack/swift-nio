package main

import (
	"errors"

	"golang.org/x/sys/unix"
)

func verifyRosetta() error {
	// must not be running under rosetta
	val, err := unix.SysctlUint32("sysctl.proc_translated")
	if err == nil && val == 1 {
		return errors.New("must not be running under Rosetta")
	}
	return nil
}
