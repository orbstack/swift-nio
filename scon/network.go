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
	"time"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/mdns"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/syncx"
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

var vnetGuestIP4 = net.ParseIP(netconf.VnetGuestIP4)
var vnetGuestIP6 = net.ParseIP(netconf.VnetGuestIP6)

type Network struct {
	bridge         *netlink.Bridge
	mtu            int
	dnsmasqProcess *os.Process
	dataDir        string

	mdnsRegistry *mdnsRegistry

	nftablesMu  syncx.Mutex
	nftForwards map[sysnet.ListenerKey]nftablesForwardMeta
	nftBlocks   map[netip.Prefix]struct{}

	hostClient *hclient.Client
}

type nftablesForwardMeta struct {
	internalPort uint16
	toMachineIP  net.IP
}

func NewNetwork(dataDir string, host *hclient.Client, db *Database, manager *ConManager) *Network {
	return &Network{
		dataDir:      dataDir,
		mdnsRegistry: newMdnsRegistry(host, db, manager),
		nftForwards:  make(map[sysnet.ListenerKey]nftablesForwardMeta),
		nftBlocks:    make(map[netip.Prefix]struct{}),
		hostClient:   host,
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

	err = setupDomainProxyInterface(mtu)
	if err != nil {
		return err
	}

	// not explicitly necesary for normal use, since host bridge will get added when vmconfig is set
	// however, if scon restarts, that host bridge will disappear and needs to be readded
	runOne("refresh host bridge", func() error {
		logrus.Debug("refreshing host bridge")
		return n.hostClient.RefreshHostBridge(false)
	})

	// add docker table route, used for docker route bit
	// ip route add default via 198.19.249.2 dev conbr0 table 64
	err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: bridge.Index,
		Gw:        sconDocker4,
		Table:     netconf.VmRouteTableDocker,
	})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	// ip route add default via fd07:b51a:cc66:0::2 dev conbr0 table 64
	err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: bridge.Index,
		Gw:        sconDocker6,
		Table:     netconf.VmRouteTableDocker,
	})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}

	// start dnsmasq
	logrus.Debug("starting dnsmasq")
	proc, err := n.spawnDnsmasq()
	if err != nil {
		return err
	}
	n.dnsmasqProcess = proc

	// apply nftables
	err = nft.ApplyConfig(nft.ConfigVM, map[string]string{
		"IF_VNET":                           ifVnet,
		"IF_VMNET_MACHINE":                  ifVmnetMachine,
		"IF_BRIDGE":                         ifBridge,
		"SCON_WEB_INDEX_IP4":                netconf.SconWebIndexIP4,
		"SCON_WEB_INDEX_IP6":                netconf.SconWebIndexIP6,
		"SCON_HOST_BRIDGE_IP4":              netconf.SconHostBridgeIP4,
		"SCON_HOST_BRIDGE_IP6":              netconf.SconHostBridgeIP6,
		"MAC_DOCKER":                        MACAddrDocker,
		"VNET_SECURE_SVC_IP4":               netconf.VnetSecureSvcIP4,
		"PORT_SECURE_SVC_DOCKER_REMOTE_CTX": strconv.Itoa(ports.SecureSvcDockerRemoteCtx),
		"SCON_SUBNET4":                      netconf.SconSubnet4CIDR,
		"SCON_SUBNET6":                      netconf.SconSubnet6CIDR,
		"VNET_SUBNET4":                      netconf.VnetSubnet4CIDR,
		"VNET_SUBNET6":                      netconf.VnetSubnet6CIDR,
		"DOMAINPROXY_SUBNET4":               netconf.DomainproxySubnet4CIDR,
		"DOMAINPROXY_SUBNET6":               netconf.DomainproxySubnet6CIDR,

		"IFGROUP_ISOLATED": strconv.Itoa(netconf.VmIfGroupIsolated),

		"FWMARK_DOCKER_ROUTE_BIT":       strconv.Itoa(netconf.VmFwmarkDockerRouteBit),
		"FWMARK_LOCAL_ROUTE_BIT":        strconv.Itoa(netconf.VmFwmarkLocalRouteBit),
		"FWMARK_TPROXY_OUTBOUND_BIT":    strconv.Itoa(netconf.VmFwmarkTproxyOutboundBit),
		"FWMARK_ISOLATED_BIT":           strconv.Itoa(netconf.VmFwmarkIsolatedBit),
		"FWMARK_HAIRPIN_MASQUERADE_BIT": strconv.Itoa(netconf.VmFwmarkHairpinMasqueradeBit),

		// port forward dest
		"INTERNAL_LISTEN_IP4": netconf.VnetGuestIP4,
		"INTERNAL_LISTEN_IP6": netconf.VnetGuestIP6,
	})
	if err != nil {
		return err
	}

	// start static nftables forward for k8s server
	// avoid taking a real forward slot - it could interfere with localhost autofwd
	err = n.addDelNftablesForward("add", sysnet.ListenerKey{
		AddrPort: netip.AddrPortFrom(netipIPv4Loopback, ports.DockerMachineK8s),
		Proto:    sysnet.ProtoTCP,
	}, nftablesForwardMeta{
		internalPort: ports.GuestK8s,
		toMachineIP:  sconDocker4,
	})
	if err != nil {
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
		"--pid-file=",              // disable pid file
		"--log-facility=/dev/null", // suppress logs

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

func (n *Network) addDelNftablesForward(action string, key sysnet.ListenerKey, meta nftablesForwardMeta) error {
	mapProto := "tcp"
	if key.Proto == sysnet.ProtoUDP {
		mapProto = "udp"
	}

	mapFamily := "4"
	if meta.toMachineIP.To4() == nil {
		mapFamily = "6"
	}

	return nft.Run(action, "element", "inet", "vm", mapProto+"_port_forwards"+mapFamily, fmt.Sprintf("{ %d : %v . %d }", meta.internalPort, meta.toMachineIP, key.Port()))
}

func (n *Network) StartNftablesForward(key sysnet.ListenerKey, internalPort uint16, internalListenIP net.IP, toMachineIP net.IP) error {
	n.nftablesMu.Lock()
	defer n.nftablesMu.Unlock()

	if _, ok := n.nftForwards[key]; ok {
		return fmt.Errorf("nftables forward already exists: %s", key)
	}

	meta := nftablesForwardMeta{
		internalPort: internalPort,
		toMachineIP:  toMachineIP,
	}
	err := n.addDelNftablesForward("add", key, meta)
	if err != nil {
		return err
	}

	n.nftForwards[key] = meta
	return nil
}

func (n *Network) StopNftablesForward(key sysnet.ListenerKey) error {
	n.nftablesMu.Lock()
	defer n.nftablesMu.Unlock()

	meta, ok := n.nftForwards[key]
	if !ok {
		// normal - we always go through this path
		return nil
	}

	err := n.addDelNftablesForward("delete", key, meta)
	if err != nil {
		return err
	}

	delete(n.nftForwards, key)
	return nil
}

func (n *Network) RefreshFlowtable() error {
	// TODO: fix race if a container stops between netlink list and nftables add
	// duplicate port = EEXIST
	// TODO: stop excluding eth1. flowtable breaks NAT64 reply route
	return nft.RefreshFlowtableBridgePorts("vm", "ft", []string{ifBridge}, []string{ifVnet}, []string{ifVmnetMachine})
}

func (n *Network) Close() error {
	if n.bridge != nil {
		err := netlink.LinkDel(n.bridge)
		if err != nil {
			logrus.WithError(err).Error("failed to delete bridge")
		}
		n.bridge = nil
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
