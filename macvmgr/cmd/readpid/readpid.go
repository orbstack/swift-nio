package main

import (
	"fmt"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/flock"
)

func main() {
	pid, err := flock.ReadPid(conf.VmgrLockFile())
	if err != nil {
		panic(err)
	}

	fmt.Println(pid)
}
