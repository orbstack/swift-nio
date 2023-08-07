package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/mdns"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	ifBridge       = "conbr0"
	ifVmnetMachine = "eth1"
	ifVmnetDocker  = "eth2"

	txQueueLen = 5000

	dhcpLeaseTime4 = "48h"
	// leave room for static assignments like docker
	dhcpLeaseStart = 10
	dhcpLeaseEnd   = 250
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

	mdnsRegistry mdnsRegistry
}

func NewNetwork(dataDir string) *Network {
	return &Network{
		dataDir:      dataDir,
		mdnsRegistry: newMdnsRegistry(),
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

	// start mDNS server
	logrus.Debug("starting mDNS server")
	iface, err := net.InterfaceByName(ifBridge) // scon bridge / machines interface
	if err != nil {
		return err
	}
	err = n.mdnsRegistry.StartServer(&mdns.Config{
		Zone:  &n.mdnsRegistry,
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

	// add ip
	addr, err := netlink.ParseAddr(netconf.SconGatewayIP4 + "/24")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}

	// add ipv6
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
	cleanup4, err := setupOneNat(iptables.ProtocolIPv4, netconf.SconSubnet4CIDR, netconf.SecureSvcIP4)
	if err != nil {
		return nil, err
	}

	cleanup6, err := setupOneNat(iptables.ProtocolIPv6, netconf.SconSubnet6CIDR, "")
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

func setupOneNat(proto iptables.Protocol, netmask string, secureSvcIP string) (func() error, error) {
	ipt, err := iptables.New(iptables.IPFamily(proto), iptables.Timeout(10))
	if err != nil {
		return nil, err
	}

	// NAT
	// filtering by output interface fixes multicast
	// can't filter by input interface (-i) in POSTROUTING
	rules := [][]string{{"nat", "POSTROUTING", "-s", netmask, "!", "-o", ifBridge, "-j", "MASQUERADE"}}

	if secureSvcIP != "" {
		// allow secureSvcIP:SecureSvcDockerRemoteCtx from docker
		// TODO this needs ip/mac spoofing protection
		rules = append(rules, []string{"filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-s", netconf.SconDockerIP4, "-d", secureSvcIP, "--dport", strconv.Itoa(ports.SecureSvcDockerRemoteCtx), "-j", "ACCEPT"})

		// block other secure svc
		rules = append(rules, []string{"filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", secureSvcIP, "-j", "REJECT", "--reject-with", "tcp-reset"})
	}

	// first, accept related/established
	rules = append(rules, []string{"filter", "INPUT", "-i", ifBridge, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"})

	// then block machines from accessing VM init-net servesr that are intended for host vmgr to connect to
	// blocked on both guest IP (198.19.248.2) and bridge gateway (198.19.249.1)
	rules = append(rules, []string{"filter", "INPUT", "-i", ifBridge, "--proto", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"})

	// add rules
	for _, rule := range rules {
		err = ipt.AppendUnique(rule[0], rule[1], rule[2:]...)
		if err != nil {
			return nil, err
		}
	}

	return func() error {
		// iterate in reverse order
		var errs []error
		for i := len(rules) - 1; i >= 0; i-- {
			rule := rules[i]
			err = ipt.DeleteIfExists(rule[0], rule[1], rule[2:]...)
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
