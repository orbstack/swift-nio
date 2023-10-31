package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/mdns"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	ifBridge       = "conbr0"
	ifVnet         = "eth0"
	ifVmnetMachine = "eth1"
	ifVmnetDocker  = "eth2"

	txQueueLen = 5000

	dhcpLeaseTime4 = "48h"
	// leave room for static assignments like docker
	dhcpLeaseStart = 10
	dhcpLeaseEnd   = 247
	raInterval     = 8 * time.Hour
	raLifetime     = 30 * 24 * time.Hour

	oomScoreAdjCriticalHost = "-950"
)

type Network struct {
	bridge         *netlink.Bridge
	mtu            int
	cleanupNAT     func() error
	dnsmasqProcess *os.Process
	dataDir        string

	mdnsRegistry *mdnsRegistry

	iptablesMu  sync.Mutex
	iptForwards map[sysnet.ListenerKey]iptablesForwardMeta
	iptBlocks   map[netip.Prefix]struct{}
}

type iptablesForwardMeta struct {
	internalPort     uint16
	internalListenIP net.IP
	toMachineIP      net.IP
}

func NewNetwork(dataDir string, host *hclient.Client, db *Database) *Network {
	return &Network{
		dataDir:      dataDir,
		mdnsRegistry: newMdnsRegistry(host, db),
		iptForwards:  make(map[sysnet.ListenerKey]iptablesForwardMeta),
		iptBlocks:    make(map[netip.Prefix]struct{}),
	}
}

func (n *Network) Start() error {
	mtu, err := getDefaultMTU()
	if err != nil {
		return err
	}
	n.mtu = mtu

	logrus.Debug("creating bridge")
	bridge, err := newBridge(mtu)
	if err != nil {
		return err
	}
	n.bridge = bridge

	// start dnsmasq
	logrus.Debug("starting dnsmasq")
	proc, err := n.spawnDnsmasq()
	if err != nil {
		return err
	}
	n.dnsmasqProcess = proc

	// configure NAT
	cleanupNAT, err := setupAllNat()
	if err != nil {
		return err
	}
	n.cleanupNAT = cleanupNAT

	// start static iptables forward for k8s server
	// avoid taking a real forward slot - it could interfere with localhost autofwd
	err = n.toggleIptablesForward("-A", sysnet.ListenerKey{
		AddrPort: netip.AddrPortFrom(netipIPv4Loopback, ports.DockerMachineK8s),
		Proto:    sysnet.ProtoTCP,
	}, iptablesForwardMeta{
		internalPort:     ports.GuestK8s,
		internalListenIP: net.ParseIP(netconf.VnetGuestIP4),
		toMachineIP:      sconDocker4,
	})
	if err != nil {
		return err
	}

	// add nat64 route:
	// ip route add default via 198.19.249.2 dev conbr0 table 64
	err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: bridge.Index,
		Gw:        sconDocker4,
		Table:     64,
	})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}

	// start mDNS server
	logrus.Debug("starting mDNS server")
	iface, err := net.InterfaceByName(ifBridge) // scon bridge / machines interface
	if err != nil {
		return err
	}
	err = n.mdnsRegistry.StartServer(&mdns.Config{
		Zone:  n.mdnsRegistry,
		Iface: iface,
	})
	if err != nil {
		return err
	}

	return nil
}

func (n *Network) spawnDnsmasq() (*os.Process, error) {
	args := []string{
		"--keep-in-foreground",
		"--bind-interfaces",
		"--strict-order",
		"--pid-file=", // disable pid file

		"--listen-address=" + netconf.SconGatewayIP4,
		"--listen-address=" + netconf.SconGatewayIP6,
		"--interface=" + ifBridge,
		"--no-ping", // LXD: prevent delays in lease file updates

		"--port=0", // disable DNS

		// IPv4 DHCP
		"--dhcp-rapid-commit",
		"--dhcp-authoritative",
		"--dhcp-no-override",
		"--dhcp-leasefile=" + path.Join(n.dataDir, "dnsmasq.leases"),
		fmt.Sprintf("--dhcp-range=%s.%d,%s.%d,%s", netconf.SconSubnet4, dhcpLeaseStart, netconf.SconSubnet4, dhcpLeaseEnd, dhcpLeaseTime4),
		"--dhcp-option=option:dns-server," + conf.C().DNSServer, // DNS
		"--dhcp-option-force=26," + strconv.Itoa(n.mtu),         // MTU

		// IPv6 SLAAC
		"--enable-ra",
		"--dhcp-range=::,constructor:" + ifBridge + ",ra-only,infinite",
		fmt.Sprintf("--ra-param=%s,mtu:%d,%d,%d", ifBridge, n.mtu, int(raInterval.Seconds()), int(raLifetime.Seconds())),

		// no debug
		"--quiet-dhcp",
		"--quiet-dhcp6",
		"--quiet-ra",
	}
	cmd := exec.Command("dnsmasq", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	// oom score adj (can't be restarted, broken network if killed)
	// do it before wait so pid is guaranteed to exist, even if zombie
	err = os.WriteFile("/proc/"+strconv.Itoa(cmd.Process.Pid)+"/oom_score_adj", []byte(oomScoreAdjCriticalHost), 0644)
	if err != nil {
		return nil, err
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			// ignore signals
			if exitErr, ok := err.(*exec.ExitError); ok {
				if exitErr.ExitCode() == -1 {
					return
				}
			}

			logrus.WithError(err).Error("dnsmasq exited with error")
		}
	}()

	return cmd.Process, nil
}

func (n *Network) toggleIptablesForward(action string, key sysnet.ListenerKey, meta iptablesForwardMeta) error {
	// MASQUERADE not needed
	// this preserves source IP from host vnet, which works due to ip forward
	return util.Run(iptCmdFor(key.Addr()),
		"-t", "nat",
		action, "PREROUTING",
		"-i", ifVnet,
		"-d", meta.internalListenIP.String(),
		"-p", string(key.Proto),
		"--dport", strconv.Itoa(int(meta.internalPort)),
		"-j", "DNAT",
		"--to-destination", net.JoinHostPort(meta.toMachineIP.String(), strconv.Itoa(int(key.Port()))))
}

func (n *Network) StartIptablesForward(key sysnet.ListenerKey, internalPort uint16, internalListenIP net.IP, toMachineIP net.IP) error {
	n.iptablesMu.Lock()
	defer n.iptablesMu.Unlock()

	if _, ok := n.iptForwards[key]; ok {
		return fmt.Errorf("iptables forward already exists: %s", key)
	}

	meta := iptablesForwardMeta{
		internalPort:     internalPort,
		internalListenIP: internalListenIP,
		toMachineIP:      toMachineIP,
	}
	err := n.toggleIptablesForward("-A", key, meta)
	if err != nil {
		return err
	}

	n.iptForwards[key] = meta
	return nil
}

func (n *Network) StopIptablesForward(key sysnet.ListenerKey) error {
	n.iptablesMu.Lock()
	defer n.iptablesMu.Unlock()

	meta, ok := n.iptForwards[key]
	if !ok {
		// normal - we always go through this path
		return nil
	}

	err := n.toggleIptablesForward("-D", key, meta)
	if err != nil {
		return err
	}

	delete(n.iptForwards, key)
	return nil
}

func iptCmdFor(addr netip.Addr) string {
	// avoid 4-in-6 issues
	if !addr.Is4() {
		return "ip6tables"
	}
	return "iptables"
}

func (n *Network) BlockIptablesForward(prefix netip.Prefix) error {
	// to prevent issues if we actually decide to use routing in the future, we only block it for outgoing traffic
	// also filter by docker machine. it's an edge case, but machines should actually be allowed to forward to containers (e.g. *.local), at least until we add ip routes to VM. need to filter by MAC instead of IP because ip forward keeps source IP
	err := util.Run(iptCmdFor(prefix.Addr()), "-t", "filter", "-I", "FORWARD", "-i", ifBridge, "-o", ifVnet, "-d", prefix.String(), "-m", "mac", "--mac-source", MACAddrDocker, "-j", "DROP")
	if err != nil {
		return err
	}

	n.iptablesMu.Lock()
	defer n.iptablesMu.Unlock()
	n.iptBlocks[prefix] = struct{}{}
	return nil
}

func (n *Network) unblockIptablesForwardLockedBase(prefix netip.Prefix) error {
	return util.Run(iptCmdFor(prefix.Addr()), "-t", "filter", "-D", "FORWARD", "-i", ifBridge, "-o", ifVnet, "-d", prefix.String(), "-m", "mac", "--mac-source", MACAddrDocker, "-j", "DROP")
}

func (n *Network) UnblockIptablesForward(prefix netip.Prefix) error {
	err := n.unblockIptablesForwardLockedBase(prefix)
	if err != nil {
		return err
	}

	n.iptablesMu.Lock()
	defer n.iptablesMu.Unlock()
	delete(n.iptBlocks, prefix)
	return nil
}

func (n *Network) ClearIptablesForwardBlocks() error {
	n.iptablesMu.Lock()
	defer n.iptablesMu.Unlock()

	for prefix := range n.iptBlocks {
		err := n.unblockIptablesForwardLockedBase(prefix)
		if err != nil {
			return err
		}
	}

	clear(n.iptBlocks)
	return nil
}

func (n *Network) Close() error {
	if n.bridge != nil {
		err := netlink.LinkDel(n.bridge)
		if err != nil {
			logrus.WithError(err).Error("failed to delete bridge")
		}
		n.bridge = nil
	}
	if n.cleanupNAT != nil {
		err := n.cleanupNAT()
		if err != nil {
			logrus.WithError(err).Error("failed to cleanup NAT")
		}
		n.cleanupNAT = nil
	}
	if n.dnsmasqProcess != nil {
		err := n.dnsmasqProcess.Kill()
		if err != nil {
			logrus.WithError(err).Error("failed to kill dnsmasq")
		}
		n.dnsmasqProcess = nil
	}
	err := n.mdnsRegistry.StopServer()
	if err != nil {
		logrus.WithError(err).Error("failed to shutdown mDNS server")
	}
	return nil
}

func newBridge(mtu int) (*netlink.Bridge, error) {
	la := netlink.NewLinkAttrs()
	la.Name = ifBridge
	la.MTU = mtu
	la.TxQLen = txQueueLen
	bridge := &netlink.Bridge{LinkAttrs: la}
	err := netlink.LinkAdd(bridge)
	if err != nil && errors.Is(err, unix.EEXIST) {
		logrus.Debug("bridge already exists, recreating")
		err = netlink.LinkDel(bridge)
		if err != nil {
			return nil, err
		}
		err = netlink.LinkAdd(bridge)
	}
	if err != nil {
		return nil, err
	}

	// add gateway,web IP
	addr, err := netlink.ParseAddr(netconf.SconGatewayIP4 + "/24")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}

	// add gateway,web IPv6
	addr, err = netlink.ParseAddr(netconf.SconGatewayIP6 + "/64")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}

	// set up
	err = netlink.LinkSetUp(bridge)
	if err != nil {
		return nil, err
	}

	// attach machine vmnet to bridge
	vmnet, err := netlink.LinkByName(ifVmnetMachine)
	if err != nil {
		return nil, err
	}

	err = netlink.LinkSetMaster(vmnet, bridge)
	if err != nil {
		return nil, err
	}

	return bridge, nil
}

func setupAllNat() (func() error, error) {
	cleanup4, err := setupOneNat(iptables.ProtocolIPv4, netconf.SconSubnet4CIDR, netconf.VnetSecureSvcIP4, netconf.SconHostBridgeIP4, netconf.SconWebIndexIP4)
	if err != nil {
		return nil, err
	}

	cleanup6, err := setupOneNat(iptables.ProtocolIPv6, netconf.SconSubnet6CIDR, "", netconf.SconHostBridgeIP6, netconf.SconWebIndexIP6)
	if err != nil {
		_ = cleanup4()
		return nil, err
	}

	return func() error {
		err1 := cleanup4()
		err2 := cleanup6()
		if err1 != nil {
			return err1
		}
		return err2
	}, nil
}

func setupOneNat(proto iptables.Protocol, netmask string, secureSvcIP string, hostBridgeIP string, webIndexIP string) (func() error, error) {
	ipt, err := iptables.New(iptables.IPFamily(proto), iptables.Timeout(10))
	if err != nil {
		return nil, err
	}

	// flush it. we own iptables
	err = ipt.ClearAll()
	if err != nil {
		return nil, err
	}

	// NAT: gvisor only accepts packets with our source IP
	// filtering by output interface fixes multicast
	// can't filter by input interface (-i) in POSTROUTING
	rules := [][]string{{"nat", "POSTROUTING", "-s", netmask, "-o", ifVnet, "-j", "MASQUERADE"}}

	// related/established
	rules = append(rules, []string{"filter", "INPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"})

	// localhost
	rules = append(rules, []string{"filter", "INPUT", "-i", "lo", "-j", "ACCEPT"})

	// host: allow mac gvisor vnet to access anything
	rules = append(rules, []string{"filter", "INPUT", "-i", ifVnet, "-j", "ACCEPT"})

	// icmp: allow all ICMP for ping and neighbor solicitation
	if proto == iptables.ProtocolIPv4 {
		rules = append(rules, []string{"filter", "INPUT", "-p", "icmp", "-j", "ACCEPT"})
	} else {
		rules = append(rules, []string{"filter", "INPUT", "-p", "icmpv6", "-j", "ACCEPT"})
	}

	// 67/udp: allow machines to use DHCP v4 server (dnsmasq)
	if proto == iptables.ProtocolIPv4 {
		rules = append(rules, []string{"filter", "INPUT", "-i", ifBridge, "-p", "udp", "--dport", "67", "-j", "ACCEPT"})
	}

	// 5353/udp: allow machines to use mDNS server
	rules = append(rules, []string{"filter", "INPUT", "-i", ifBridge, "-p", "udp", "--dport", "5353", "-j", "ACCEPT"})

	// allow mac host bridge to access web server ports 80 and 443
	// block machines because it could leak info to isolated machines
	// TODO this needs ip/mac spoofing protection
	rules = append(rules, []string{"filter", "INPUT", "-i", ifBridge, "-s", hostBridgeIP, "-d", webIndexIP, "--proto", "tcp", "-m", "multiport", "--dports", "80,443", "-j", "ACCEPT"})

	// explicitly block machines from accessing VM init-net servers that are intended for host vmgr to connect to
	rules = append(rules, []string{"filter", "INPUT", "-i", ifBridge, "--proto", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"})

	/*
	 * forward
	 */

	// limit access to secure services
	if secureSvcIP != "" {
		// allow secureSvcIP:SecureSvcDockerRemoteCtx from docker
		// will be covered by MAC protection from isolated machines in the future
		// non-isolated machines can already access any socket by running commands on host
		rules = append(rules, []string{"filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-m", "mac", "--mac-source", MACAddrDocker, "-d", secureSvcIP, "--dport", strconv.Itoa(ports.SecureSvcDockerRemoteCtx), "-j", "ACCEPT"})

		// block other secure svc
		rules = append(rules, []string{"filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", secureSvcIP, "-j", "REJECT", "--reject-with", "tcp-reset"})
		// and for UDP and other protocols too
		rules = append(rules, []string{"filter", "FORWARD", "-i", ifBridge, "-d", secureSvcIP, "-j", "DROP"})
	}

	// allow machines to access any internet address, via gvisor
	// this includes private IPs and everything else
	// don't allow forwarding to any other interfaces we may add to machine in the future
	rules = append(rules, []string{"filter", "FORWARD", "-i", ifBridge, "-o", ifVnet, "-j", "ACCEPT"})
	// reverse forward uses MASQUERADE but it's still subject to FORWARD
	// can't filter by ctstate ESTABLISHED/RELATED because DNAT port forwards use this path too
	rules = append(rules, []string{"filter", "FORWARD", "-i", ifVnet, "-o", ifBridge, "-j", "ACCEPT"})

	// now, the real purpose of policy=DROP for FORWARD is to prevent routing loops
	// to do so, we prepend/delete rules when creating bridges
	// Linux will never ip-forward conbr0 subnet to eth0 since it's a local route on conbr0, so no need to worry about that. only vlan bridges are at risk because the netns is different, and because we do L3 forwarding for them

	// allow machines to talk to each other, and to macOS host bridge IP
	// this is bridge but still counts as forward
	rules = append(rules, []string{"filter", "FORWARD", "-i", ifBridge, "-o", ifBridge, "-j", "ACCEPT"})

	/*
	 * raw security
	 */

	// reverse path filter for internal services
	// prevents machines from hijacking existing internal TCP conns
	// we don't do this for all IPs for performance, and because it could cause issues with NAT64 fib routing
	// but the vnet subnet is usually not perf critical
	// rule: raw -> [conntrack] -> mangle -> nat -> filter
	if proto == iptables.ProtocolIPv4 {
		rules = append(rules, []string{"raw", "PREROUTING", "-i", ifBridge, "-d", netconf.VnetSubnet4CIDR, "-m", "rpfilter", "--invert", "-j", "DROP"})
	} else {
		rules = append(rules, []string{"raw", "PREROUTING", "-i", ifBridge, "-d", netconf.VnetSubnet6CIDR, "-m", "rpfilter", "--invert", "-j", "DROP"})
	}

	// add rules
	for _, rule := range rules {
		err = ipt.Append(rule[0], rule[1], rule[2:]...)
		if err != nil {
			return nil, err
		}
	}

	// rules added. it's now safe to change policies to DROP
	// INPUT: policy DROP for security.
	err = ipt.ChangePolicy("filter", "INPUT", "DROP")
	if err != nil {
		return nil, err
	}

	return func() error {
		var errs []error
		// revert policy
		err := ipt.ChangePolicy("filter", "INPUT", "ACCEPT")
		if err != nil {
			errs = append(errs, err)
		}

		// iterate in reverse order
		for i := len(rules) - 1; i >= 0; i-- {
			rule := rules[i]
			err := ipt.Delete(rule[0], rule[1], rule[2:]...)
			if err != nil {
				errs = append(errs, err)
			}
		}

		return errors.Join(errs...)
	}, nil
}

func getDefaultMTU() (int, error) {
	// get default interface index
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return 0, err
	}
	var defaultRoute *netlink.Route
	for _, r := range routes {
		if r.Dst == nil {
			defaultRoute = &r
			break
		}
	}
	if defaultRoute == nil {
		return 0, errors.New("no default route")
	}

	// get default interface
	link, err := netlink.LinkByIndex(defaultRoute.LinkIndex)
	if err != nil {
		return 0, err
	}
	return link.Attrs().MTU, nil
}

func deriveMacAddress(cid string) string {
	// hash of id
	h := sha256.Sum256([]byte(cid))
	// mark as locally administered
	h[0] |= 0x02
	// mark as unicast
	h[0] &= 0xfe
	// format
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", h[0], h[1], h[2], h[3], h[4], h[5])
}
