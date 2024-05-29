package main

import (
	"os"
	"strconv"

	"github.com/orbstack/macvirt/scon/sclient"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
)

const (
	lxcHookPostStop = "post-stop"
)

func runLxcPostStop(cid string) {
	// don't bother to close it, we're exiting anyway
	addr := vnetGuestIP4.String() + ":" + strconv.Itoa(ports.GuestScon)
	client, err := sclient.New("tcp", addr)
	check(err)

	err = client.InternalReportStopped(cid)
	check(err)

	os.Exit(0)
}

func runLxcHook() {
	hook := os.Args[2]
	cid := os.Args[3]

	switch hook {
	case lxcHookPostStop:
		runLxcPostStop(cid)
	default:
		panic("unknown hook: " + hook)
	}
}
