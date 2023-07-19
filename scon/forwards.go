package main

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

const (
	// match chrony ntp polling interval
	autoForwardGCInterval  = 128 * time.Second
	autoForwardGCThreshold = 1 * time.Minute
	autoForwardDebounce    = 250 * time.Millisecond

	// special case for systemd-network DHCP client, and Debian's LLMNR
	portDHCPClient = 68
	portLLMNR      = 5355
)

var (
	netipIPv4Loopback = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	netipIPv6Loopback = netip.AddrFrom16([16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})

	netipSubnet4 = netip.MustParsePrefix(netconf.SconSubnet4CIDR)
	netipSubnet6 = netip.MustParsePrefix(netconf.SconSubnet6CIDR)
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

func (m *ConManager) addForwardCLocked(c *Container, spec sysnet.ProcListener) (retErr error) {
	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"spec":      spec,
	}).Info("add forward")

	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	// already there?
	if _, ok := m.forwards[spec]; ok {
		return errors.New("forward already exists")
	}

	// block port on container side
	if c.bpf != nil {
		err := c.bpf.LfwdBlockPort(spec.Port)
		if err != nil {
			return err
		}
		defer func() {
			if retErr != nil {
				c.bpf.LfwdUnblockPort(spec.Port)
			}
		}()
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
		listener, err := netx.ListenTCP("tcp", &net.TCPAddr{
			IP:   internalListenIP, // only on NIC
			Port: 0,                // random
		})
		if err != nil {
			return err
		}
		internalPort = uint16(listener.Addr().(*net.TCPAddr).Port)

		// pass to agent
		err = c.useAgentLocked(func(a *agent.Client) error {
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
			err2 := c.useAgentLocked(func(a *agent.Client) error {
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
		err = c.useAgentLocked(func(a *agent.Client) error {
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
			err2 := c.useAgentLocked(func(a *agent.Client) error {
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
		"owner":        c.Name,
	}).Debug("forward added")
	return nil
}

func (m *ConManager) removeForwardCLocked(c *Container, spec sysnet.ProcListener) error {
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
	err = c.useAgentLocked(func(a *agent.Client) error {
		switch spec.Proto {
		case sysnet.ProtoTCP:
			return a.StopProxyTCP(agentSpec)
		case sysnet.ProtoUDP:
			return a.StopProxyUDP(agentSpec)
		default:
			return errors.New("unknown protocol")
		}
	})
	if err != nil && !errors.Is(err, ErrMachineNotRunning) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrUnexpectedEOF) {
		// proceed with removal - worst case, we leak a socket, but still remove forward entry to allow reuse
		logrus.WithField("container", c.Name).WithError(err).Error("failed to stop agent side of forward")
		return err
	}

	// unblock port on container side
	if c.bpf != nil {
		err := c.bpf.LfwdUnblockPort(spec.Port)
		if err != nil {
			logrus.WithField("container", c.Name).WithError(err).Error("failed to unblock port")
		}
	}

	delete(m.forwards, spec)
	return nil
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

// triggered by bpf ptrack
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
		return ErrMachineNotRunning
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

	added, removed := util.DiffSlices(c.lastListeners, listeners)

	var errs []error
	var notAdded []sysnet.ProcListener
	var notRemoved []sysnet.ProcListener
	for _, listener := range added {
		err := c.manager.addForwardCLocked(c, listener)
		if err != nil {
			errs = append(errs, err)
			notAdded = append(notAdded, listener)
			continue
		}
	}

	for _, listener := range removed {
		err := c.manager.removeForwardCLocked(c, listener)
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
