package main

import (
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"golang.org/x/sys/unix"
)

func verifyAPFS() error {
	testPath := conf.RunDir() + "/.apfs-test"
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
