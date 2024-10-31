package main

import (
	"os"
	"strconv"

	"github.com/orbstack/macvirt/scon/sclient"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/vishvananda/netlink"
)

const (
	lxcHookPostStop = "post-stop"
	lxcHookNetIfUp  = "net-if-up"
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

func runLxcNetIfUp(isIsolated bool) {
	ifName := os.Getenv("LXC_NET_PEER")
	if ifName == "" {
		panic("LXC_NET_PEER not set")
	}

	link, err := netlink.LinkByName(ifName)
	check(err)

	if isIsolated {
		err = netlink.LinkSetGroup(link, netconf.VmIfGroupIsolated)
		check(err)
	}

	// we need hairpin on bridges so that domainproxy works from a machine to itself
	err = netlink.LinkSetHairpin(link, true)
	check(err)

	// we don't want to generate any link local addresses on the bridgeports
	err = os.WriteFile("/proc/sys/net/ipv6/conf/"+ifName+"/addr_gen_mode", []byte("1"), 0)
	check(err)

	// we need ipv6 enabled on the bridgeports because broute means iif gets attributed to the bridgeports
	// broute is needed to make hairpin work
	// if ipv6 is disabled, ipv6 packets automatically get dropped
	err = os.WriteFile("/proc/sys/net/ipv6/conf/"+ifName+"/disable_ipv6", []byte("0"), 0)
	check(err)
}

func runLxcHook() {
	hook := os.Args[2]
	cid := os.Args[3]

	switch hook {
	case lxcHookPostStop:
		runLxcPostStop(cid)
	case lxcHookNetIfUp:
		isIsolated := os.Args[4] == "true"
		runLxcNetIfUp(isIsolated)
	default:
		panic("unknown hook: " + hook)
	}
}
