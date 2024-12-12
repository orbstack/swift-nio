// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package bridge

import (
	"net/netip"
	"sync"
	"time"

	"github.com/orbstack/macvirt/vmgr/swext"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/util/simplerate"
	"github.com/sirupsen/logrus"
)

const (
	// according to Apple docs, limit is 4 per VM and 32 globally on host
	// we can theoretically get up to 128 (7 bits)
	// but empirically, max for our process is 10
	// scon machine bridge takes 1 so we can have up to 9 more bridges
	// and we're reserving one for potential future use (bridged net, NAT, etc)
	MaxVlanInterfaces = 8
	IndexSconMachine  = MaxVlanInterfaces
)

// the shorter, the less packet loss
const routeChangeDebounce = 100 * time.Millisecond

func NewRouteMon() (*RouteMon, error) {
	m := &RouteMon{
		// rate limit to break infinite loop if we're fighting with VPN
		// 10 req in 8 sec
		renewLimiter: simplerate.NewLimiter(10, 8*time.Second),
	}
	m.renewDebounce = syncx.NewFuncDebounce(routeChangeDebounce, func() {
		// we have 2 anti-feedback-loop precautions:
		// 1. NWPathMonitor excludes
		// 2. rate limiter prevents excessive looping
		// so allow queuing renewals via mutex to avoid missing events in quick succession
		// with AF_ROUTE, missing it is OK because another route event will be triggered by InternetSharing route change when renewal finishes, but not with NWPathMonitor
		m.renewMu.Lock()
		defer m.renewMu.Unlock()

		logrus.Debug("checking for renew")

		// check mDNS listener interfaces
		if m.onRefreshMdns != nil {
			err := m.onRefreshMdns()
			if err != nil {
				logrus.WithError(err).Error("failed to refresh mDNS")
			}
		}

		// get routing table once (full table needed to make accurate decision)
		routingTable, err := getRoutingTable()
		if err != nil {
			logrus.WithError(err).Error("failed to get routing table")
			return
		}

		// only take rate limit token if we have something to renew
		var ratelimitTaken bool
		ratelimitPredicate := func() bool {
			if ratelimitTaken {
				return true
			}
			ratelimitTaken = true
			return m.renewLimiter.Allow()
		}

		var wg sync.WaitGroup
		m.subnetsMu.Lock()
		for i := range m.subnets {
			// value reference
			ratelimited := m.subnets[i].maybeRenewAsync(&wg, routingTable, ratelimitPredicate)
			if ratelimited {
				logrus.WithField("subnet", m.subnets[i]).Warn("giving up on bridge: rate limit exceeded")
				break
			}
		}
		m.subnetsMu.Unlock()
		// wait for completion before releasing mutexes
		wg.Wait()

		logrus.Debug("renewal done")
	})

	return m, nil
}

type RouteMon struct {
	// pretty fast - usually not many networks and just a few mask ops, no need for LPM tree
	subnetsMu syncx.Mutex
	// +1 for scon machines
	// value type for fast iteration on each route packet
	subnets [MaxVlanInterfaces + 1]MonitoredSubnet

	renewMu       syncx.Mutex // separate mutex to prevent deadlock
	renewLimiter  *simplerate.Limiter
	renewDebounce syncx.FuncDebounce

	onRefreshMdns func() error
}

// can't import hostmdns due to import cycle
func (m *RouteMon) SetOnRefreshMdns(fn func() error) {
	m.onRefreshMdns = fn
}

func (m *RouteMon) Close() error {
	m.ClearVlanSubnets()
	m.ClearSubnet(IndexSconMachine)
	return nil
}

func (m *RouteMon) Monitor() error {
	for range swext.NetPathChangesChan {
		m.renewDebounce.Call()
	}

	return nil
}

// ContainsIP returns whether ip is in any of the actively monitored bridge subnets.
func (m *RouteMon) ContainsIP(ip netip.Addr) bool {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	for _, subnet := range m.subnets {
		if !subnet.IsActive() {
			continue
		}

		if subnet.prefix4.Contains(ip) || subnet.prefix6.Contains(ip) {
			return true
		}
	}

	return false
}

func (m *RouteMon) SetSubnet(index int, prefix4 netip.Prefix, prefix6 netip.Prefix, renewFn func() error) error {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	subnet := &m.subnets[index]
	// this clears
	*subnet = NewMonitoredSubnet(prefix4, prefix6, renewFn)
	return nil
}

func (m *RouteMon) ClearSubnet(index int) {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	m.subnets[index].Clear()
}

func (m *RouteMon) ClearVlanSubnets() {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	for i := range m.subnets {
		if i == IndexSconMachine {
			continue
		}

		m.subnets[i].Clear()
	}
}
