package vnet

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/tcpfwd"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/udpfwd"
)

func (n *Network) StartForward(fromSpec, toSpec string) error {
	fromProto, fromAddr, ok := strings.Cut(fromSpec, ":")
	if !ok {
		return fmt.Errorf("invalid fromSpec: %s", fromSpec)
	}

	toProto, toPort, ok := strings.Cut(toSpec, ":")
	if !ok {
		return fmt.Errorf("invalid toSpec: %s", toSpec)
	}

	isInternal := true
	switch fromProto {
	case "tcp":
		switch toProto {
		case "tcp":
			connectAddr4 := GuestIP4 + ":" + toPort
			connectAddr6 := "[" + GuestIP6 + "]:" + toPort
			err := tcpfwd.StartTcpHostForward(n.Stack, n.NIC, GatewayIP4, GatewayIP6, fromAddr, connectAddr4, connectAddr6, isInternal)
			if err != nil {
				return err
			}
		case "vsock":
			vsockPort, err := strconv.ParseUint(toPort, 10, 32)
			if err != nil {
				return err
			}
			dialer := func() (net.Conn, error) {
				return n.VsockDialer(uint32(vsockPort))
			}
			err = tcpfwd.StartTcpVsockHostForward(fromAddr, dialer)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported protocols: %s -> %s", fromProto, toProto)
		}
	case "udp":
		switch toProto {
		case "udp":
			connectAddr4 := GuestIP4 + ":" + toPort
			connectAddr6 := "[" + GuestIP6 + "]:" + toPort
			err := udpfwd.StartUDPHostForward(n.Stack, fromAddr, connectAddr4, connectAddr6)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported protocols: %s -> %s", fromProto, toProto)
		}
	case "unix":
		// delete socket first
		_ = os.Remove(fromAddr)
		switch toProto {
		case "tcp":
			connectAddr4 := GuestIP4 + ":" + toPort
			err := tcpfwd.StartUnixTcpHostForward(n.Stack, n.NIC, fromAddr, connectAddr4)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported protocols: %s -> %s", fromProto, toProto)
		}
	default:
		return fmt.Errorf("unsupported protocol: %s", fromProto)
	}

	return nil
}
