package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/rpc"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	autoForwardDebounce = 20 * time.Millisecond

	// special case for systemd-network DHCP client, and Debian's LLMNR
	portDHCPClient = 68
	portMDNS       = 5353
	portLLMNR      = 5355
)

var (
	netipIPv4Loopback = netip.AddrFrom4([4]byte{127, 0, 0, 1})
	netipIPv6Loopback = netip.IPv6Loopback()
)

type ForwardState struct {
	InternalPort    uint16
	HostForwardSpec hclient.ForwardSpec
}

func procToAgentSpec(key sysnet.ListenerKey) agent.ProxySpec {
	return agent.ProxySpec{
		IsIPv6: key.Addr().Is6(),
		Port:   key.Port(),
	}
}

func filterListener(l sysnet.ListenerInfo) bool {
	// remove DHCP client, mDNS, and LLMNR
	// mDNS won't work b/c mDNSResponder occupies it on macOS
	if l.Proto == sysnet.ProtoUDP && (l.Port() == portDHCPClient || l.Port() == portMDNS || l.Port() == portLLMNR) {
		return false
	}

	// only forward 0.0.0.0/:: and 127.0.0.1/::1
	// so this excludes systemd-resolved, bridge-only, etc.
	return l.Addr() == netipIPv4Loopback || l.Addr() == netipIPv6Loopback || l.Addr().IsUnspecified()
}

func filterListeners(listeners []sysnet.ListenerInfo, forceK8sLocalhost bool) []sysnet.ListenerInfo {
	var filtered []sysnet.ListenerInfo
	for _, l := range listeners {
		if filterListener(l) {
			// special case: k8s port should have ext listen addr of localhost
			if forceK8sLocalhost {
				// 10250 == kubelet metrics
				// TODO: what's the ephemeral port listening on ::?
				if l.Port() == ports.HostKubernetes || l.Port() == 10250 {
					// mismatch with internal (Which is v6 due to '::' in machine), but it still works fine
					l.ExtListenAddr = netipIPv4Loopback
					// and let's pretend that the internal listener is v4, to fix mismatch
					// dial will still work due to :: tcp46 listener on k8s side
					l.ListenerKey.AddrPort = netip.AddrPortFrom(netip.IPv4Unspecified(), l.Port())
				}
			}

			filtered = append(filtered, l)
		}
	}
	return filtered
}

// pstub is, unfortunately, the only way to do this safely, race-free.
// dockerd starts userland proxy to reserve the port, *before* it adds iptables rules
// so if we look at iptables too soon, rule won't be there
// and if we do it later after pmon's nft-change trigger, the forward may already have been set up, so we won't check UseIptables again
func addContainerNftablesForward(c *Container, spec sysnet.ListenerInfo, internalPort uint16, internalListenIP net.IP) error {
	var toMachineIP netip.Addr
	var err error
	if spec.Addr().Is4() {
		toMachineIP, err = c.getIP4()
	} else {
		toMachineIP, err = c.getIP6()
	}
	if err != nil {
		return fmt.Errorf("get container IP: %w", err)
	}

	err = c.manager.net.StartNftablesForward(spec.ListenerKey, internalPort, toMachineIP)
	if err != nil {
		return err
	}

	return nil
}

func (m *ConManager) addForward(c *Container, rt *ContainerRuntimeState, spec sysnet.ListenerInfo) (retErr error) {
	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"spec":      spec,
	}).Info("add forward")

	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	// already there?
	if _, ok := m.forwards[spec.ListenerKey]; ok {
		return errors.New("forward already exists")
	}

	// block port on container side
	if rt.bpf != nil {
		err := rt.bpf.LfwdBlockPort(spec.Port())
		if err != nil {
			return err
		}
		defer func() {
			if retErr != nil {
				rt.bpf.LfwdUnblockPort(spec.Port())
			}
		}()
	}

	targetPort := spec.Port() // container and external macOS port are the same
	agentSpec := procToAgentSpec(spec.ListenerKey)
	var internalListenIP net.IP
	if spec.Addr().Is4() {
		internalListenIP = vnetGuestIP4
	} else {
		internalListenIP = vnetGuestIP6
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
		var agentResult agent.ProxyResult
		err = rt.UseAgent(func(a *agent.Client) error {
			r, err := a.StartProxyTCP(agentSpec, listener)
			agentResult = r
			return err
		})
		// if it succeeded, we don't need this anymore. agent has the fd
		// if it failed, we need to close it to prevent a leak
		listener.Close()
		if err != nil {
			return err
		}
		defer func() {
			if retErr != nil {
				err2 := rt.UseAgent(func(a *agent.Client) error {
					return a.StopProxyTCP(agentSpec)
				})
				if err2 != nil {
					logrus.WithError(err2).Error("failed to stop tcp proxy after error")
				}
			}
		}()

		// enable iptables acceleration if eligible (soft fail)
		// if we do this later, first conn could be slow
		if spec.UseNftables() || agentResult.IsDockerPstub {
			err = addContainerNftablesForward(c, spec, internalPort, internalListenIP)
			if err != nil {
				logrus.WithError(err).Error("failed to add nftables forward")
			} else {
				defer func() {
					if retErr != nil {
						err2 := m.net.StopNftablesForward(spec.ListenerKey)
						if err2 != nil {
							logrus.WithError(err2).Error("failed to stop nftables forward after error")
						}
					}
				}()
			}
		}

		// tell host
		hostForwardSpec = hclient.ForwardSpec{
			Host:  "tcp:" + net.JoinHostPort(hostListenIP, strconv.Itoa(int(targetPort))),
			Guest: "tcp:" + strconv.Itoa(int(internalPort)),
		}
		err = m.host.StartForward(hostForwardSpec)
		if err != nil {
			return fmt.Errorf("host: %w", err)
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
		var agentResult agent.ProxyResult
		err = rt.UseAgent(func(a *agent.Client) error {
			r, err := a.StartProxyUDP(agentSpec, listener)
			agentResult = r
			return err
		})
		// if it succeeded, we don't need this anymore
		// if it failed, we need to close it to prevent a leak
		listener.Close()
		if err != nil {
			return err
		}
		defer func() {
			if retErr != nil {
				err2 := rt.UseAgent(func(a *agent.Client) error {
					return a.StopProxyUDP(agentSpec)
				})
				if err2 != nil {
					logrus.WithError(err2).Error("failed to stop udp proxy after error")
				}
			}
		}()

		// enable iptables acceleration if eligible (soft fail)
		// if we do this later, first conn could be slow
		// this is especially important for UDP because userspace UDP proxy is subject to timeouts
		if spec.UseNftables() || agentResult.IsDockerPstub {
			err = addContainerNftablesForward(c, spec, internalPort, internalListenIP)
			if err != nil {
				logrus.WithError(err).Error("failed to add nftables forward")
			} else {
				defer func() {
					if retErr != nil {
						err2 := m.net.StopNftablesForward(spec.ListenerKey)
						if err2 != nil {
							logrus.WithError(err2).Error("failed to stop nftables forward after error")
						}
					}
				}()
			}
		}

		// tell host
		hostForwardSpec = hclient.ForwardSpec{
			Host:  "udp:" + net.JoinHostPort(hostListenIP, strconv.Itoa(int(targetPort))),
			Guest: "udp:" + strconv.Itoa(int(internalPort)),
		}
		err = m.host.StartForward(hostForwardSpec)
		if err != nil {
			return fmt.Errorf("host: %w", err)
		}
	}

	m.forwards[spec.ListenerKey] = ForwardState{
		InternalPort:    internalPort,
		HostForwardSpec: hostForwardSpec,
	}
	rt.activeForwards[spec.ListenerKey] = struct{}{}

	logrus.WithFields(logrus.Fields{
		"spec":         spec,
		"internalPort": internalPort,
		"internalAddr": internalListenIP,
		"host":         hostForwardSpec,
		"owner":        c.Name,
	}).Debug("forward added")
	return nil
}

func (m *ConManager) removeForward(c *Container, rt *ContainerRuntimeState, key sysnet.ListenerKey) error {
	logrus.WithField("key", key).Info("remove forward")

	// check active forwards
	if _, ok := rt.activeForwards[key]; !ok {
		// was not successfully added as a forward, so nothing to remove
		return nil
	}

	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	// check owner
	state, ok := m.forwards[key]
	if !ok {
		return fmt.Errorf("container '%s' owns forward '%v' but it doesn't exist?!", c.Name, key)
	}

	// tell host
	err := m.host.StopForward(state.HostForwardSpec)
	if err != nil {
		return fmt.Errorf("host: %w", err)
	}

	// remove nftables acceleration
	// spec.UseIptables / pstub state might change, so look up by key and remove it if it exists
	err = m.net.StopNftablesForward(key)
	if err != nil {
		return fmt.Errorf("nft: %w", err)
	}

	// tell agent (our side of listener is already closed)
	agentSpec := procToAgentSpec(key)
	err = rt.UseAgent(func(a *agent.Client) error {
		switch key.Proto {
		case sysnet.ProtoTCP:
			return a.StopProxyTCP(agentSpec)
		case sysnet.ProtoUDP:
			return a.StopProxyUDP(agentSpec)
		default:
			return errors.New("unknown protocol")
		}
	})
	if err != nil && !errors.Is(err, ErrMachineNotRunning) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, rpc.ErrShutdown) {
		// proceed with removal - worst case, we leak a socket, but still remove forward entry to allow reuse
		logrus.WithField("container", c.Name).WithError(err).Error("failed to stop agent side of forward")
	}

	// unblock port on container side
	if rt.bpf != nil {
		err = rt.bpf.LfwdUnblockPort(key.Port())
		if err != nil {
			logrus.WithField("container", c.Name).WithError(err).Error("failed to unblock port")
		}
	}

	delete(rt.activeForwards, key)
	delete(m.forwards, key)
	return nil
}

func readOneIptablesListeners(ipVer int, listeners []sysnet.ListenerInfo, forceRestrictLocalhost bool) ([]sysnet.ListenerInfo, error) {
	cmd := "iptables"
	if ipVer == 6 {
		cmd = "ip6tables"
	}

	rulesStr, err := util.RunWithOutput(cmd, "-t", "nat", "-S")
	if err != nil {
		return nil, err
	}

	for _, rule := range strings.Split(rulesStr, "\n") {
		parts := strings.Split(rule, " ")
		if len(parts) < 4 {
			continue
		}

		// must be KUBE-NODEPORTS (for NodePort) or KUBE-SERVICES (for LoadBalancer)
		// ClusterIP is handled separately
		if parts[0] != "-A" || (parts[1] != "KUBE-NODEPORTS" && parts[1] != "KUBE-SERVICES") {
			continue
		}

		// if there's a destination filter, it must be docker machine IP
		// this makes sure we only detect LoadBalancer ports in KUBE-SERVICES
		// KUBE-NODEPORTS doesn't have destination filters
		destIndex := slices.Index(parts, "-d")
		if destIndex != -1 && destIndex+1 < len(parts) {
			destCIDR := parts[destIndex+1]
			if destCIDR != netconf.SconDockerIP4+"/32" && destCIDR != netconf.SconDockerIP6+"/128" {
				continue
			}
		}

		// find "-p"
		protoIndex := slices.Index(parts, "-p")
		if protoIndex == -1 || protoIndex+1 >= len(parts) {
			continue
		}
		proto := parts[protoIndex+1]
		if proto != "tcp" && proto != "udp" {
			continue
		}

		// find "--dport"
		dportIndex := slices.Index(parts, "--dport")
		if dportIndex == -1 || dportIndex+1 >= len(parts) {
			continue
		}
		port, err := strconv.Atoi(parts[dportIndex+1])
		if err != nil {
			continue
		}

		unspecAddr := netip.IPv4Unspecified()
		if ipVer == 6 {
			unspecAddr = netip.IPv6Unspecified()
		}

		// add listener to list
		listener := sysnet.ListenerInfo{
			// nodeports are technically always 0.0.0.0 b/c of how we configured kube-proxy
			// but let's restrict them to localhost for security
			ListenerKey: sysnet.ListenerKey{
				AddrPort: netip.AddrPortFrom(unspecAddr, uint16(port)),
				Proto:    sysnet.TransportProtocol(proto),
			},
			// always safe b/c 0.0.0.0 and source IP already lost
			FromIptables: true,
		}
		if forceRestrictLocalhost {
			if ipVer == 4 {
				listener.ExtListenAddr = netipIPv4Loopback
			} else {
				listener.ExtListenAddr = netipIPv6Loopback
			}
		}
		listeners = append(listeners, listener)
	}

	return listeners, nil
}

// even if we don't need forwards to mac, it's important to register these listeneres as forwards so they get added to lfwd blocked_ports map
// otherwise route_localnet doesn't work and these ports don't work in the machine
func (c *Container) readIptablesListeners(listeners []sysnet.ListenerInfo, forceRestrictLocalhost bool) ([]sysnet.ListenerInfo, error) {
	return withContainerNetns(c, func() ([]sysnet.ListenerInfo, error) {
		// v4 and v6
		listeners, err := readOneIptablesListeners(4, listeners, forceRestrictLocalhost)
		if err != nil {
			return nil, err
		}

		listeners, err = readOneIptablesListeners(6, listeners, forceRestrictLocalhost)
		if err != nil {
			return nil, err
		}

		return listeners, nil
	})
}

// workaround for generics not working for value types
func diffSlicesListenerKey(old, new []sysnet.ListenerInfo) (removed, added []sysnet.ListenerInfo) {
	oldMap := make(map[sysnet.ListenerKey]struct{}, len(old))
	for _, item := range old {
		oldMap[item.Identifier()] = struct{}{}
	}
	newMap := make(map[sysnet.ListenerKey]struct{}, len(new))
	for _, item := range new {
		newMap[item.Identifier()] = struct{}{}
	}

	for _, newItem := range new {
		if _, ok := oldMap[newItem.Identifier()]; !ok {
			added = append(added, newItem)
		}
	}
	for _, oldItem := range old {
		if _, ok := newMap[oldItem.Identifier()]; !ok {
			removed = append(removed, oldItem)
		}
	}

	return
}

// triggered by bpf pmon
func (rt *ContainerRuntimeState) updateListenersNow(c *Container) error {
	// TODO: use flags to reduce update work
	rt.listenerDirtyFlags.Store(0)

	newListeners, err := sysnet.ReadAllProcNetFromDirfs(rt.InitProcDirfd)
	if err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ESRCH) || errors.Is(err, os.ErrClosed) {
			// normal: machine is no longer running
			return nil
		} else {
			return err
		}
	}

	if c.ID == ContainerIDK8s && c.manager.k8sEnabled {
		// add nodeports from iptables
		newListeners, err = c.readIptablesListeners(newListeners, !c.manager.k8sExposeServices /*forceRestrictLocalhost*/)
		if err != nil {
			return fmt.Errorf("read iptables: %w", err)
		}
	}

	newListeners = filterListeners(newListeners, c.ID == ContainerIDK8s && !c.manager.k8sExposeServices /*forceK8sLocalhost*/)
	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"listeners": newListeners,
	}).Debug("update listeners")

	removed, added := diffSlicesListenerKey(rt.listeners, newListeners)

	// must remove before adding in case of recreate with conflicting IP within debounce period
	for _, listener := range removed {
		err := c.manager.removeForward(c, rt, listener.ListenerKey)
		if err != nil {
			logrus.WithField("container", c.Name).WithError(err).Error("failed to remove forward")
		}
	}
	for _, listener := range added {
		err := c.manager.addForward(c, rt, listener)
		if err != nil {
			logrus.WithField("container", c.Name).WithError(err).Error("failed to add forward")
		}
	}

	rt.listeners = newListeners
	return nil
}

func (c *Container) triggerListenersUpdate(dirtyFlags bpf.ListenerTypeFlags) {
	rt, err := c.RuntimeState()
	if err != nil {
		return
	}
	rt.listenerDirtyFlags.Or(uint32(dirtyFlags))
	rt.listenersDebounce.Call()
}
