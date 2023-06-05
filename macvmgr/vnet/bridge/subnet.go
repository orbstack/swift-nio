package bridge

import (
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// the shorter, the less packet loss
const routeChangeDebounce = 100 * time.Millisecond

type MonitoredSubnet struct {
	// for matching
	prefix4 netip.Prefix
	prefix6 netip.Prefix

	// everything else lives in this closure
	debounce *syncx.FuncDebounce
}

func (m *MonitoredSubnet) IsActive() bool {
	return m.debounce != nil
}

func (m *MonitoredSubnet) Match(addr netip.Addr) bool {
	return m.IsActive() && (m.prefix4.Contains(addr) || m.prefix6.Contains(addr))
}

func (m *MonitoredSubnet) Clear() {
	if m.IsActive() {
		m.debounce.Cancel()
		m.debounce = nil
	}
}

// host IP for UDP dial to check route
func NewMonitoredSubnet(prefix4 netip.Prefix, prefix6 netip.Prefix, hostIP net.IP, renewFn func() error) MonitoredSubnet {
	// rate limit to break infinite loop if we're fighting with VPN
	// avg 2 req/s, burst 3 (so effectively we can exhaust quota within 1 sec)
	renewLimiter := rate.NewLimiter(2, 3)

	var renewMu sync.Mutex
	debounce := syncx.NewFuncDebounce(routeChangeDebounce, func() {
		// ignore if we're already renewing (to avoid feedback loop)
		if !renewMu.TryLock() {
			return
		}
		defer renewMu.Unlock()

		if !renewLimiter.Allow() {
			logrus.WithField("prefix4", prefix4).Debug("route renew rate limited")
			return
		}

		// check and skip if route is OK
		correct, err := isRouteCorrect(hostIP)
		if err != nil {
			logrus.WithField("prefix4", prefix4).WithError(err).Error("failed to check host bridge route")
			return
		}
		if correct {
			return
		}

		err = renewFn()
		if err != nil {
			logrus.WithField("prefix4", prefix4).WithError(err).Error("failed to renew host bridge")
		}
	})

	return MonitoredSubnet{
		prefix4:  prefix4,
		prefix6:  prefix6,
		debounce: &debounce,
	}
}
