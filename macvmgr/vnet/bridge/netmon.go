// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package bridge

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

const (
	// a bit under macOS limit of 32
	// we can theoretically get up to 128 (7 bits)
	MaxVlanInterfaces = 24

	IndexSconMachine = MaxVlanInterfaces
)

const verboseDebug = false

func NewRouteMon() (*RouteMon, error) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, 0)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)

	// use netpoll so we can close and return from read loop
	err = unix.SetNonblock(fd, true)
	if err != nil {
		return nil, err
	}

	return &RouteMon{
		file: os.NewFile(uintptr(fd), "[route]"),
	}, nil
}

type RouteMon struct {
	file *os.File // AF_ROUTE socket
	buf  [2 << 10]byte

	// pretty fast - usually not many networks and just a few mask ops, no need for LPM tree
	subnetsMu sync.Mutex
	// +1 for scon machines
	// value type for fast iteration on each route packet
	subnets [MaxVlanInterfaces + 1]MonitoredSubnet
}

func (m *RouteMon) Close() error {
	m.ClearSubnets()
	m.file.Close()
	return nil
}

func (m *RouteMon) Monitor() error {
	for {
		n, err := m.file.Read(m.buf[:])
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return nil
			} else {
				return err
			}
		}
		msgs, err := route.ParseRIB(route.RIBTypeRoute, m.buf[:n])
		if err != nil {
			logrus.WithError(err).Error("failed to parse route message")
			continue
		}
		if len(msgs) == 0 {
			continue
		}

		// onMessage needs lock, so just take it for all msgs
		m.subnetsMu.Lock()
		for _, msg := range msgs {
			m.onMessage(msg)
		}
		m.subnetsMu.Unlock()
	}
}

func (m *RouteMon) onMessage(msg route.Message) {
	if msg, ok := msg.(*route.RouteMessage); ok {
		// check type
		if msg.Type != unix.RTM_ADD && msg.Type != unix.RTM_DELETE && msg.Type != unix.RTM_CHANGE {
			return
		}

		// check if it involves our subnets
		addr := ipOfAddr(addrType(msg.Addrs, unix.RTAX_DST))
		for _, subnet := range m.subnets {
			if subnet.Match(addr) {
				subnet.debounce.Call()

				if verboseDebug {
					m.logMessages([]route.Message{msg})
				}
			}
		}
	}
}

func (m *RouteMon) SetSubnet(index int, prefix4 netip.Prefix, prefix6 netip.Prefix, hostIP net.IP, renewFn func() error) error {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	subnet := &m.subnets[index]
	subnet.Clear()
	*subnet = NewMonitoredSubnet(prefix4, prefix6, hostIP, renewFn)
	return nil
}

func (m *RouteMon) ClearSubnet(index int) {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	m.subnets[index].Clear()
}

func (m *RouteMon) ClearSubnets() {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	for i := range m.subnets {
		m.subnets[i].Clear()
	}
}

// addrType returns addrs[rtaxType], if that (the route address type) exists,
// else it returns nil.
//
// The RTAX_* constants at https://github.com/apple/darwin-xnu/blob/main/bsd/net/route.h
// for what each address index represents.
func addrType(addrs []route.Addr, rtaxType int) route.Addr {
	if len(addrs) > rtaxType {
		return addrs[rtaxType]
	}
	return nil
}

// ipOfAddr returns the route.Addr (possibly nil) as a netip.Addr
// (possibly zero).
func ipOfAddr(a route.Addr) netip.Addr {
	switch a := a.(type) {
	case *route.Inet4Addr:
		return netip.AddrFrom4(a.IP)
	case *route.Inet6Addr:
		ip := netip.AddrFrom16(a.IP)
		if a.ZoneID != 0 {
			ip = ip.WithZone(fmt.Sprint(a.ZoneID)) // TODO: look up net.InterfaceByIndex? but it might be changing?
		}
		return ip
	}
	return netip.Addr{}
}
