package vnet

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/netconf"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/tcpfwd"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/udpfwd"
)

type HostForward interface {
	io.Closer
}

type ForwardSpec struct {
	Host  string
	Guest string
}

func (n *Network) StartForward(spec ForwardSpec) error {
	n.hostForwardMu.Lock()
	defer n.hostForwardMu.Unlock()

	if _, ok := n.hostForwards[spec.Host]; ok {
		return fmt.Errorf("forward already exists: %s", spec.Host)
	}

	fromProto, fromAddr, ok := strings.Cut(spec.Host, ":")
	if !ok {
		return fmt.Errorf("invalid spec.From: %s", spec.Host)
	}

	toProto, toPort, ok := strings.Cut(spec.Guest, ":")
	if !ok {
		return fmt.Errorf("invalid spec.To: %s", spec.Guest)
	}

	isInternal := true
	var fwd HostForward
	var err error
	switch fromProto {
	case "tcp":
		switch toProto {
		case "tcp":
			connectAddr4 := netconf.GuestIP4 + ":" + toPort
			connectAddr6 := "[" + netconf.GuestIP6 + "]:" + toPort
			fwd, err = tcpfwd.StartTcpHostForward(n.Stack, n.NIC, netconf.GatewayIP4, netconf.GatewayIP6, fromAddr, connectAddr4, connectAddr6, isInternal)
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
			fwd, err = tcpfwd.StartTcpVsockHostForward(fromAddr, dialer)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported protocols: %s -> %s", fromProto, toProto)
		}
	case "udp":
		switch toProto {
		case "udp":
			connectAddr4 := netconf.GuestIP4 + ":" + toPort
			connectAddr6 := "[" + netconf.GuestIP6 + "]:" + toPort
			fwd, err = udpfwd.StartUDPHostForward(n.Stack, fromAddr, connectAddr4, connectAddr6)
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
			connectAddr4 := netconf.GuestIP4 + ":" + toPort
			fwd, err = tcpfwd.StartUnixTcpHostForward(n.Stack, n.NIC, fromAddr, connectAddr4)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported protocols: %s -> %s", fromProto, toProto)
		}
	default:
		return fmt.Errorf("unsupported protocol: %s", fromProto)
	}

	n.hostForwards[spec.Host] = fwd
	return nil
}

func (n *Network) StopForward(spec ForwardSpec) error {
	n.hostForwardMu.Lock()
	defer n.hostForwardMu.Unlock()

	fwd, ok := n.hostForwards[spec.Host]
	if !ok {
		return fmt.Errorf("forward not found: %s", spec.Host)
	}

	err := fwd.Close()
	if err != nil {
		return err
	}

	delete(n.hostForwards, spec.Host)

	return nil
}
