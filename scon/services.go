package main

import (
	"net"
	"strconv"

	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/services/readyevents/readyclient"
)

func listenAndReportReady(network string, service string, host string, port uint16) (net.Listener, error) {
	listener, err := net.Listen(network, net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}

	// the listener backlog acts as holding pen until we call Accept
	readyclient.ReportReady(netconf.VnetSecureSvcIP4, service)
	return listener, nil
}
