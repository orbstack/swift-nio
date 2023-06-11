// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package bridge

import (
	"net"
	"net/netip"
	"sync"

	"github.com/orbstack/macvirt/macvmgr/vzf"
)

const (
	// a bit under macOS limit of 32
	// we can theoretically get up to 128 (7 bits)
	MaxVlanInterfaces = 24

	IndexSconMachine = MaxVlanInterfaces
)

func NewRouteMon() (*RouteMon, error) {
	return &RouteMon{}, nil
}

type RouteMon struct {
	// pretty fast - usually not many networks and just a few mask ops, no need for LPM tree
	subnetsMu sync.Mutex
	// +1 for scon machines
	// value type for fast iteration on each route packet
	subnets [MaxVlanInterfaces + 1]MonitoredSubnet
}

func (m *RouteMon) Close() error {
	m.ClearSubnets()
	return nil
}

func (m *RouteMon) Monitor() error {
	for {
		<-vzf.SwextNetPathChangesChan

		// onMessage needs lock, so just take it for all msgs
		m.subnetsMu.Lock()
		for _, subnet := range m.subnets {
			if subnet.IsActive() {
				subnet.debounce.Call()
			}
		}
		m.subnetsMu.Unlock()
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
