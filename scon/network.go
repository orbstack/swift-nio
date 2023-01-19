package main

import (
	"errors"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	ifBridge = "sconbr0"
)

func newBridge() (*netlink.Bridge, error) {
	la := netlink.NewLinkAttrs()
	la.Name = ifBridge
	la.MTU = 1500
	la.TxQLen = 10000
	bridge := &netlink.Bridge{LinkAttrs: la}
	err := netlink.LinkAdd(bridge)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = netlink.LinkDel(bridge)
			if err != nil {
				return nil, err
			}
		}
		return nil, err
	}
	// add ip
	addr, err := netlink.ParseAddr("172.30.31.1/24")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}
	// add ipv6
	addr, err = netlink.ParseAddr("fc00:30:31::1/64")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}
	// set up
	err = netlink.LinkSetUp(bridge)
	if err != nil {
		return nil, err
	}
	return bridge, nil
}

func setupNat() (func() error, error) {
	ipt, err := iptables.New(iptables.IPFamily(iptables.ProtocolIPv4), iptables.Timeout(5))
	if err != nil {
		return nil, err
	}

	// TODO interface?
	err = ipt.AppendUnique("nat", "POSTROUTING", "-s", "172.30.31.0/24", "!", "-d", "172.30.31.0/24", "-j", "MASQUERADE")
	if err != nil {
		return nil, err
	}

	err = ipt.AppendUnique("filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", "172.30.30.201", "-j", "REJECT", "--reject-with", "tcp-reset")
	if err != nil {
		return nil, err
	}

	return func() error {
		err = ipt.DeleteIfExists("nat", "POSTROUTING", "-s", "172.30.31.0/24", "!", "-d", "172.30.31.0/24", "-j", "MASQUERADE")
		if err != nil {
			return err
		}

		err = ipt.DeleteIfExists("filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", "172.30.30.201", "-j", "REJECT", "--reject-with", "tcp-reset")
		if err != nil {
			return err
		}

		return nil
	}, nil
}
