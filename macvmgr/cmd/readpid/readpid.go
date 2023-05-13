package main

import (
	"fmt"

	"github.com/orbstack/macvirt/macvmgr/conf"
	"github.com/orbstack/macvirt/macvmgr/flock"
)

func main() {
	pid, err := flock.ReadPid(conf.VmgrLockFile())
	if err != nil {
		panic(err)
	}

	fmt.Println(pid)
}
