package main

import (
	"net"
	"os"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/services/readyevents/readyclient"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	iocKrpcPassconn = 0x8004da02
)

func RunKrpcInitiator() error {
	listener, err := listenAndReportReady("tcp", readyclient.ServiceKrpc, netconf.VnetGuestIP4, ports.GuestKrpc)
	if err != nil {
		return err
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}

		err = func(conn net.Conn) error {
			defer conn.Close()

			// submit fd to kernel
			devFile, err := os.Open("/dev/krpc")
			if err != nil {
				return err
			}
			defer devFile.Close()

			connFile, err := conn.(*net.TCPConn).File()
			if err != nil {
				return err
			}
			defer connFile.Close()

			err = unix.IoctlSetInt(int(devFile.Fd()), iocKrpcPassconn, int(connFile.Fd()))
			if err != nil {
				return err
			}

			return nil
		}(conn)
		if err != nil {
			logrus.WithError(err).Error("krpc: failed to pass conn")
		}
	}
}
