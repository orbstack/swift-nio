package main

import (
	"fmt"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/flock"
)

func main() {
	pid, err := flock.ReadPid(conf.VmgrLockFile())
	if err != nil {
		panic(err)
	}

	fmt.Println(pid)
}
