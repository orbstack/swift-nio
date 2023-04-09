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
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/kdrag0n/macvirt/scon/util/sysnet"
	"github.com/sirupsen/logrus"
)

const (
	autoForwardGCInterval  = 2 * time.Minute
	autoForwardGCThreshold = 1 * time.Minute
	autoForwardDebounce    = 250 * time.Millisecond

	// special case for systemd-network DHCP client, and Debian's LLMNR
	portDHCPClient = 68
	portLLMNR      = 5355
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

func procToAgentSpec(p sysnet.ProcListener) agent.ProxySpec {
	return agent.ProxySpec{
		IsIPv6: p.Addr.Is6(),
		Port:   p.Port,
	}
}

func filterListener(l sysnet.ProcListener) bool {
	// remove DHCP client and LLMNR
	if l.Proto == sysnet.ProtoUDP && (l.Port == portDHCPClient || l.Port == portLLMNR) {
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

func filterListeners(listeners []sysnet.ProcListener) []sysnet.ProcListener {
	var filtered []sysnet.ProcListener
	for _, l := range listeners {
		if filterListener(l) {
			filtered = append(filtered, l)
		}
	}
	return filtered
}

func (m *ConManager) addForward(c *Container, spec sysnet.ProcListener) error {
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
		internalListenIP = util.DefaultAddress4()
	} else {
		internalListenIP = util.DefaultAddress6()
	}
	hostListenIP := spec.HostListenIP()

	var internalPort uint16
	var hostForwardSpec hclient.ForwardSpec
	switch spec.Proto {
	case sysnet.ProtoTCP:
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
		err = c.UseAgent(func(a *agent.Client) error {
			return a.StartProxyTCP(agentSpec, listener)
		})
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
			err2 := c.UseAgent(func(a *agent.Client) error {
				return a.StopProxyTCP(agentSpec)
			})
			if err2 != nil {
				logrus.WithError(err2).Error("failed to stop tcp proxy after hcontrol error")
			}
			return err
		}

	case sysnet.ProtoUDP:
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
		err = c.UseAgent(func(a *agent.Client) error {
			return a.StartProxyUDP(agentSpec, listener)
		})
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
			err2 := c.UseAgent(func(a *agent.Client) error {
				return a.StopProxyUDP(agentSpec)
			})
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

func (m *ConManager) removeForward(c *Container, spec sysnet.ProcListener) error {
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
	err = state.Owner.UseAgent(func(a *agent.Client) error {
		switch spec.Proto {
		case sysnet.ProtoTCP:
			return a.StopProxyTCP(agentSpec)
		case sysnet.ProtoUDP:
			return a.StopProxyUDP(agentSpec)
		default:
			return errors.New("unknown protocol")
		}
	})
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}

	delete(m.forwards, spec)
	return nil
}

func (m *ConManager) checkForward(c *Container, spec sysnet.ProcListener) bool {
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
func (c *Container) updateListenersNow() error {
	// this is to prevent stopping while we're updating listeners
	c.mu.Lock()
	defer c.mu.Unlock()

	// if scheduled before stop
	if !c.runningLocked() {
		return nil
	}

	initPid := c.lxc.InitPid()
	if initPid < 0 {
		return ErrNotRunning
	}

	listeners, err := sysnet.ReadAllProcNet(strconv.Itoa(initPid))
	if err != nil {
		return err
	}
	listeners = filterListeners(listeners)
	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"listeners": listeners,
	}).Debug("update listeners")

	added, removed := diffSlices(c.lastListeners, listeners)

	var errs []error
	var notAdded []sysnet.ProcListener
	var notRemoved []sysnet.ProcListener
	for _, listener := range added {
		err := c.manager.addForward(c, listener)
		if err != nil {
			errs = append(errs, err)
			notAdded = append(notAdded, listener)
			continue
		}
	}

	for _, listener := range removed {
		err := c.manager.removeForward(c, listener)
		if err != nil {
			errs = append(errs, err)
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
	return errors.Join(errs...)
}

func (c *Container) triggerListenersUpdate() {
	c.autofwdDebounce.Call()
}

func (m *ConManager) runWatchdogGC() {
	ticker := time.NewTicker(autoForwardGCInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.containersMu.RLock()
			for _, c := range m.containersByID {
				// watchdog: verify container state
				running := c.Running()
				lxcRunning := c.lxcRunning()
				// make sure it matches our status
				if running != lxcRunning {
					// we did that without locking so this isn't necessarily correct. take a closer look
					go func(c *Container) {
						c.mu.RLock()
						lockedRunning := c.Running()
						lockedState := c.State()
						hasMismatch := lockedRunning != (lockedState == types.ContainerStateRunning)
						c.mu.RUnlock()

						if !hasMismatch {
							// false positive, we were in the middle of a state change
							return
						}

						logrus.WithFields(logrus.Fields{
							"container": c.Name,
							"running":   running,
							"state":     c.State(),
						}).Warn("watchdog: container state mismatch, refreshing")
						err := c.refreshState()
						if err != nil {
							logrus.WithField("container", c.Name).WithError(err).Error("watchdog: failed to refresh container state")
						}
					}(c)
				}

				if !running {
					continue
				}

				go func(c *Container) {
					c.mu.RLock()
					lastAutofwdUpdate := c.lastAutofwdUpdate
					c.mu.RUnlock()

					if time.Since(lastAutofwdUpdate) > autoForwardGCThreshold {
						err := c.updateListenersNow()
						if err != nil {
							logrus.WithField("container", c.Name).WithError(err).Error("failed to GC listeners")
						}
					}
				}(c)
			}
			m.containersMu.RUnlock()
		case <-m.stopChan:
			return
		}
	}
}
