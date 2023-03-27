package main

import (
	"errors"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"golang.org/x/sys/unix"
)

func verifyAPFS() error {
	testPath := conf.DataDir() + "/.apfs-test"
	testPath2 := testPath + ".2"

	f, err := os.Create(testPath)
	if err != nil {
		return err
	}
	f.Close()
	defer os.Remove(testPath)

	err = unix.Clonefile(testPath, testPath2, 0)
	if err != nil {
		return err
	}
	defer os.Remove(testPath2)

	return nil
}

func verifyRosetta() error {
	// must not be running under rosetta
	val, err := unix.SysctlUint32("sysctl.proc_translated")
	if err == nil && val == 1 {
		return errors.New("must not be running under Rosetta")
	}
	return nil
}
