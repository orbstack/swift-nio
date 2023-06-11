package bridge

import (
	"fmt"
	"net"
	"sync"

	"github.com/sirupsen/logrus"
)

type MonitoredSubnet struct {
	hostIP4 net.IP
	hostIP6 net.IP
	renewFn func() error
}

func (m *MonitoredSubnet) IsActive() bool {
	return m.renewFn != nil
}

func (m *MonitoredSubnet) Clear() {
	m.hostIP4 = nil
	m.hostIP6 = nil
	m.renewFn = nil
}

func (m *MonitoredSubnet) maybeRenewAsync(wg *sync.WaitGroup) {
	if !m.IsActive() {
		return
	}
	fmt.Println("active one:", m)

	// check and skip if both v4 and v6 routes are OK
	correct4, err := isRouteCorrect(m.hostIP4)
	if err != nil {
		logrus.WithField("ip4", m.hostIP4).WithError(err).Error("failed to check host bridge route")
		return
	}
	correct6 := true
	if m.hostIP6 != nil {
		correct6, err = isRouteCorrect(m.hostIP6)
		if err != nil {
			logrus.WithField("ip6", m.hostIP6).WithError(err).Error("failed to check host bridge route")
			return
		}
	}
	if correct4 && correct6 {
		return
	}

	// proceeding with renewal, in parallel with other subnets
	wg.Add(1)
	// save renew fn reference before releasing mutex
	renewFn := m.renewFn
	hostIP4 := m.hostIP4
	go func() {
		defer wg.Done()

		err := renewFn()
		if err != nil {
			logrus.WithField("ip4", hostIP4).WithError(err).Error("failed to renew host bridge")
		}
	}()
}

// host IP for UDP dial to check route
func NewMonitoredSubnet(hostIP4 net.IP, hostIP6 net.IP, renewFn func() error) MonitoredSubnet {
	return MonitoredSubnet{
		hostIP4: hostIP4,
		hostIP6: hostIP6,
		renewFn: renewFn,
	}
}
