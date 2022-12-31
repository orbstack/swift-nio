package main

import (
	"runtime"

	"github.com/kdrag0n/macvirt/macvmgr/vnet"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	opts := vnet.NetOptions{
		MTU: 65520,
	}

	fd := 0 // stdin
	_, err := vnet.StartQemuFd(opts, fd)
	check(err)

	// block forever
	runtime.Goexit()
}
