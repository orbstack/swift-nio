package bridge

import (
	"net/netip"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/route"
)

type MonitoredSubnet struct {
	prefix4 netip.Prefix
	prefix6 netip.Prefix
	renewFn func() error

	lastCorrect4 *bool
	lastCorrect6 *bool
}

func (m *MonitoredSubnet) IsActive() bool {
	return m.renewFn != nil
}

func (m *MonitoredSubnet) Clear() {
	*m = MonitoredSubnet{}
}

func (m *MonitoredSubnet) maybeRenewAsync(wg *sync.WaitGroup, routingTable []route.Message, predicate func() bool) (ratelimited bool) {
	if !m.IsActive() {
		return
	}

	// check and skip if both v4 and v6 routes are OK
	correct4, err := HasValidRoute(routingTable, m.prefix4)
	if err != nil {
		logrus.WithField("ip4", m.prefix4).WithError(err).Error("failed to check host bridge route")
		return
	}
	correct6 := true
	if m.prefix6.IsValid() {
		correct6, err = HasValidRoute(routingTable, m.prefix6)
		if err != nil {
			logrus.WithField("ip6", m.prefix6).WithError(err).Error("failed to check host bridge route")
			return
		}
	}
	if verboseDebug {
		logrus.WithFields(logrus.Fields{
			"ip4": m.prefix4,
			"ip6": m.prefix6,
		}).Debugf("host bridge route check: v4=%v v6=%v", correct4, correct6)
	}
	if correct4 && correct6 {
		return
	}

	// also skip if there's no difference from last time
	// TODO: add this check back to prevent fighting with Surge
	// this breaks case where tailscale is on w/ exit node before orbstack starts, b/c before and after renew are both correct4&&correct6=false
	/*
		if m.lastCorrect4 != nil && m.lastCorrect6 != nil &&
			correct4 == *m.lastCorrect4 && correct6 == *m.lastCorrect6 {
			if verboseDebug {
				logrus.WithFields(logrus.Fields{
					"ip4": m.prefix4,
					"ip6": m.prefix6,
				}).Debugf("host bridge route: SKIP v4=%v v6=%v (no change)", correct4, correct6)
			}
			return
		}
	*/
	m.lastCorrect4 = &correct4
	m.lastCorrect6 = &correct6

	// proceeding with renewal, in parallel with other subnets
	if !predicate() {
		// take rate limit token if needed
		ratelimited = true
		return
	}
	wg.Add(1)
	// save renew fn reference before releasing mutex
	renewFn := m.renewFn
	prefix4 := m.prefix4
	go func() {
		defer wg.Done()

		// if conflict with wifi or NetworkExtension VPN (WireGuard), we get generalFailure
		err := renewFn()
		if err != nil {
			logrus.WithField("ip4", prefix4).WithError(err).Error("failed to update host bridge")
		}
	}()

	return
}

// host IP for UDP dial to check route
func NewMonitoredSubnet(prefix4 netip.Prefix, prefix6 netip.Prefix, renewFn func() error) MonitoredSubnet {
	return MonitoredSubnet{
		prefix4: prefix4,
		prefix6: prefix6,
		renewFn: renewFn,
	}
}
