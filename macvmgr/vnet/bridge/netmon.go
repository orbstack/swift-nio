// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package bridge

import (
	"net"
	"sync"
	"time"

	"github.com/orbstack/macvirt/macvmgr/vzf"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

const (
	// according to Apple docs, limit is 4 per VM and 32 globally on host
	// we can theoretically get up to 128 (7 bits)
	// but empirically, max for our process is 10
	// scon machine bridge takes 1 so we can have up to 9 docker bridges
	MaxVlanInterfaces = 9
	IndexSconMachine  = MaxVlanInterfaces
)

// the shorter, the less packet loss
const routeChangeDebounce = 100 * time.Millisecond

func NewRouteMon() (*RouteMon, error) {
	m := &RouteMon{
		// rate limit to break infinite loop if we're fighting with VPN
		// avg 2 req/s, burst 3 (so effectively we can exhaust quota within 1 sec)
		renewLimiter: rate.NewLimiter(3, 3),
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

		if !m.renewLimiter.Allow() {
			logrus.Debug("route renew: rate limited")
			return
		}

		var wg sync.WaitGroup
		m.subnetsMu.Lock()
		for i := range m.subnets {
			// value reference
			m.subnets[i].maybeRenewAsync(&wg)
		}
		m.subnetsMu.Unlock()
		// wait for completion before releasing mutexes
		wg.Wait()

		logrus.Debug("renew fn completed")
	})

	return m, nil
}

type RouteMon struct {
	// pretty fast - usually not many networks and just a few mask ops, no need for LPM tree
	subnetsMu sync.Mutex
	// +1 for scon machines
	// value type for fast iteration on each route packet
	subnets [MaxVlanInterfaces + 1]MonitoredSubnet

	renewMu       sync.Mutex // separate mutex to prevent deadlock
	renewLimiter  *rate.Limiter
	renewDebounce syncx.FuncDebounce
}

func (m *RouteMon) Close() error {
	close(vzf.SwextNetPathChangesChan)
	m.ClearSubnets()
	return nil
}

func (m *RouteMon) Monitor() error {
	for range vzf.SwextNetPathChangesChan {
		m.renewDebounce.Call()
	}

	return nil
}

func (m *RouteMon) SetSubnet(index int, hostIP4 net.IP, hostIP6 net.IP, renewFn func() error) error {
	m.subnetsMu.Lock()
	defer m.subnetsMu.Unlock()

	subnet := &m.subnets[index]
	subnet.Clear()
	*subnet = NewMonitoredSubnet(hostIP4, hostIP6, renewFn)
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
