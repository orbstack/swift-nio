package main

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/sirupsen/logrus"
)

const (
	autoForwardGCInterval  = 2 * time.Minute
	autoForwardGCThreshold = 1 * time.Minute
	autoForwardDebounce    = 250 * time.Millisecond

	// special case for systemd-network DHCP client
	portDHCPClient = 68
)

var (
	netipIPv4Loopback = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	netipIPv6Loopback = netip.AddrFrom16([16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})

	netipSubnet4 = netip.MustParsePrefix(subnet4cidr)
	netipSubnet6 = netip.MustParsePrefix(subnet6cidr)
)

type ForwardState struct {
	Owner           *Container
	InternalPort    uint16
	HostForwardSpec hclient.ForwardSpec
}

func procToAgentSpec(p agent.ProcListener) agent.ProxySpec {
	return agent.ProxySpec{
		IsIPv6: p.Addr.Is6(),
		Port:   p.Port,
	}
}

func filterListener(l agent.ProcListener) bool {
	// remove DHCP client
	if l.Proto == agent.ProtoUDP && l.Port == portDHCPClient {
		return false
	}

	// for systemd-resolved: loopback only if it's 127.0.0.1 / ::1
	if l.Addr == netipIPv4Loopback || l.Addr == netipIPv6Loopback {
		return true
	}

	// 0.0.0.0 / :: is also ok
	if l.Addr.IsUnspecified() {
		return true
	}

	// otherwise, require that it matches our subnet
	if l.Addr.Is4() {
		return netipSubnet4.Contains(l.Addr)
	} else {
		return netipSubnet6.Contains(l.Addr)
	}
}

func filterListeners(listeners []agent.ProcListener) []agent.ProcListener {
	var filtered []agent.ProcListener
	for _, l := range listeners {
		if filterListener(l) {
			filtered = append(filtered, l)
		}
	}
	return filtered
}

func (m *ConManager) addForward(c *Container, spec agent.ProcListener) error {
	logrus.WithField("spec", spec).Info("add forward")

	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	// already there?
	if _, ok := m.forwards[spec]; ok {
		return errors.New("forward already exists")
	}

	targetPort := spec.Port // container and external macOS port are the same
	agentSpec := procToAgentSpec(spec)
	var internalListenIP net.IP
	if spec.Addr.Is4() {
		internalListenIP = getDefaultAddress4()
	} else {
		internalListenIP = getDefaultAddress6()
	}
	hostListenIP := spec.HostListenIP()

	var internalPort uint16
	var hostForwardSpec hclient.ForwardSpec
	switch spec.Proto {
	case agent.ProtoTCP:
		// listen
		listener, err := net.ListenTCP("tcp", &net.TCPAddr{
			IP:   internalListenIP, // only on NIC
			Port: 0,                // random
		})
		if err != nil {
			return err
		}
		internalPort = uint16(listener.Addr().(*net.TCPAddr).Port)

		// pass to agent
		err = c.Agent().StartProxyTCP(agentSpec, listener)
		// if it succeeded, we don't need this anymore
		// if it failed, we need to close it to prevent a leak
		listener.Close()
		if err != nil {
			return err
		}

		// tell host
		hostForwardSpec = hclient.ForwardSpec{
			Host:  "tcp:" + net.JoinHostPort(hostListenIP, strconv.Itoa(int(targetPort))),
			Guest: "tcp:" + strconv.Itoa(int(internalPort)),
		}
		err = m.host.StartForward(hostForwardSpec)
		if err != nil {
			err2 := c.Agent().StopProxyTCP(agentSpec)
			if err2 != nil {
				logrus.WithError(err2).Error("failed to stop tcp proxy after hcontrol error")
			}
			return err
		}

	case agent.ProtoUDP:
		// listen
		listener, err := net.ListenUDP("udp", &net.UDPAddr{
			IP:   internalListenIP, // only on NIC
			Port: 0,                // random
		})
		if err != nil {
			return err
		}
		internalPort = uint16(listener.LocalAddr().(*net.UDPAddr).Port)

		// pass to agent
		err = c.Agent().StartProxyUDP(agentSpec, listener)
		// if it succeeded, we don't need this anymore
		// if it failed, we need to close it to prevent a leak
		listener.Close()
		if err != nil {
			return err
		}

		// tell host
		hostForwardSpec = hclient.ForwardSpec{
			Host:  "udp:" + net.JoinHostPort(hostListenIP, strconv.Itoa(int(targetPort))),
			Guest: "udp:" + strconv.Itoa(int(internalPort)),
		}
		err = m.host.StartForward(hostForwardSpec)
		if err != nil {
			err2 := c.Agent().StopProxyUDP(agentSpec)
			if err2 != nil {
				logrus.WithError(err2).Error("failed to stop udp proxy after hcontrol error")
			}
			return err
		}
	}

	m.forwards[spec] = ForwardState{
		Owner:           c,
		InternalPort:    internalPort,
		HostForwardSpec: hostForwardSpec,
	}
	logrus.WithFields(logrus.Fields{
		"spec":         spec,
		"internalPort": internalPort,
		"internalAddr": internalListenIP,
		"host":         hostForwardSpec,
	}).Debug("forward added")
	return nil
}

func (m *ConManager) removeForward(c *Container, spec agent.ProcListener) error {
	logrus.WithField("spec", spec).Info("remove forward")

	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	// check owner
	state, ok := m.forwards[spec]
	if !ok {
		return errors.New("forward does not exist")
	}
	if state.Owner != c {
		return errors.New("forward belongs to another container")
	}

	// tell host
	err := m.host.StopForward(state.HostForwardSpec)
	if err != nil {
		return err
	}

	// tell agent (our side of listener is already closed)
	agentSpec := procToAgentSpec(spec)
	switch spec.Proto {
	case agent.ProtoTCP:
		err = state.Owner.Agent().StopProxyTCP(agentSpec)
	case agent.ProtoUDP:
		err = state.Owner.Agent().StopProxyUDP(agentSpec)
	}
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}

	delete(m.forwards, spec)
	return nil
}

func (m *ConManager) checkForward(c *Container, spec agent.ProcListener) bool {
	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	// check owner
	state, ok := m.forwards[spec]
	if !ok {
		return false
	}
	if state.Owner != c {
		return false
	}

	return true
}

func diffSlices[T comparable](old, new []T) (added, removed []T) {
	oldMap := make(map[T]struct{})
	for _, listener := range old {
		oldMap[listener] = struct{}{}
	}
	newMap := make(map[T]struct{})
	for _, listener := range new {
		newMap[listener] = struct{}{}
	}

	for _, newListener := range new {
		if _, ok := oldMap[newListener]; !ok {
			added = append(added, newListener)
		}
	}
	for _, oldListener := range old {
		if _, ok := newMap[oldListener]; !ok {
			removed = append(removed, oldListener)
		}
	}

	return
}

func filterSlice[T comparable](s []T, f func(T) bool) []T {
	var out []T
	for _, v := range s {
		if f(v) {
			out = append(out, v)
		}
	}
	return out
}

func filterMapSlice[T any, N any](s []T, f func(T) (N, bool)) []N {
	var out []N
	for _, v := range s {
		if nv, ok := f(v); ok {
			out = append(out, nv)
		}
	}
	return out
}

// triggered on seccomp notify or inet diag
func (c *Container) updateListenersDirect() error {
	listeners, err := c.Agent().GetListeners()
	if err != nil {
		return err
	}
	listeners = filterListeners(listeners)
	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"listeners": listeners,
	}).Debug("update listeners")

	c.mu.Lock()
	defer c.mu.Unlock()
	added, removed := diffSlices(c.lastListeners, listeners)

	var lastErr error
	var notAdded []agent.ProcListener
	var notRemoved []agent.ProcListener
	for _, listener := range added {
		err := c.manager.addForward(c, listener)
		if err != nil {
			lastErr = err
			notAdded = append(notAdded, listener)
			continue
		}
	}

	for _, listener := range removed {
		err := c.manager.removeForward(c, listener)
		if err != nil {
			lastErr = err
			notRemoved = append(notRemoved, listener)
			continue
		}
	}

	if len(notAdded) > 0 || len(notRemoved) > 0 {
		logrus.WithFields(logrus.Fields{
			"notAdded":   notAdded,
			"notRemoved": notRemoved,
		}).Warn("failed to apply listener changes")
	}

	c.lastListeners = listeners
	c.lastAutofwdUpdate = time.Now()
	return lastErr
}

func (c *Container) triggerListenersUpdate() {
	c.autofwdDebounce.Call()
}

func (m *ConManager) runAutoForwardGC() {
	ticker := time.NewTicker(autoForwardGCInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.containersMu.RLock()
			for _, c := range m.containersByID {
				if !c.Running() {
					continue
				}

				go func(c *Container) {
					c.mu.RLock()

					// for Docker, don't GC if frozen, otherwise it'll hang forever
					if time.Since(c.lastAutofwdUpdate) > autoForwardGCThreshold && !c.IsFrozen() {
						c.mu.RUnlock()
						err := c.updateListenersDirect()
						if err != nil {
							logrus.WithField("container", c.Name).WithError(err).Error("failed to GC listeners")
						}
					} else {
						c.mu.RUnlock()
					}
				}(c)
			}
			m.containersMu.RUnlock()
		case <-m.stopChan:
			return
		}
	}
}
