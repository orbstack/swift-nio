package main

import (
	"os"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/sclient"
)

const (
	lxcHookPostStop = "post-stop"
)

func runLxcPostStop(cid string) {
	url := "http://" + getDefaultAddress4().String() + ":" + strconv.Itoa(ports.GuestScon)
	client, err := sclient.New(url)
	check(err)
	defer client.Close()

	err = client.InternalReportStopped(cid)
	check(err)
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
