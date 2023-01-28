package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	ifBridge = "conbr0"

	subnet4     = "172.30.31"
	subnet4cidr = subnet4 + ".0/24"
	gatewayIP4  = subnet4 + ".1"

	subnet6     = "fc00:30:31:"
	subnet6cidr = subnet6 + ":/64"
	gatewayIP6  = subnet6 + ":1"

	txQueueLen = 5000

	dhcpLeaseTime4 = "48h"
	raInterval     = 2 * time.Hour
	raLifetime     = 30 * 24 * time.Hour
)

type Network struct {
	bridge         *netlink.Bridge
	mtu            int
	cleanupNAT     func() error
	dnsmasqProcess *os.Process
	dataDir        string
}

func NewNetwork(dataDir string) *Network {
	return &Network{
		dataDir: dataDir,
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

	cleanupNAT, err := setupAllNat()
	if err != nil {
		return err
	}
	n.cleanupNAT = cleanupNAT

	return nil
}

func (n *Network) spawnDnsmasq() (*os.Process, error) {
	args := []string{
		"--keep-in-foreground",
		"--bind-interfaces",
		"--strict-order",
		"--pid-file=", // disable pid file

		"--listen-address=" + gatewayIP4,
		"--listen-address=" + gatewayIP6,
		"--interface=" + ifBridge,
		"--no-ping", // LXD: prevent delays in lease file updates

		"--port=0", // disable DNS

		// IPv4 DHCP
		"--dhcp-rapid-commit",
		"--dhcp-authoritative",
		"--dhcp-no-override",
		"--dhcp-leasefile=" + path.Join(n.dataDir, "dnsmasq.leases"),
		fmt.Sprintf("--dhcp-range=%s.2,%s.254,%s", subnet4, subnet4, dhcpLeaseTime4),
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

	go func() {
		err := cmd.Wait()
		if err != nil {
			// ignore SIGKILL
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
		netlink.LinkDel(n.bridge)
		n.bridge = nil
	}
	if n.cleanupNAT != nil {
		n.cleanupNAT()
		n.cleanupNAT = nil
	}
	if n.dnsmasqProcess != nil {
		n.dnsmasqProcess.Kill()
		n.dnsmasqProcess = nil
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
	addr, err := netlink.ParseAddr(gatewayIP4 + "/24")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}

	// add ipv6
	addr, err = netlink.ParseAddr(gatewayIP6 + "/64")
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
	return bridge, nil
}

func setupAllNat() (func() error, error) {
	cleanup4, err := setupOneNat(iptables.ProtocolIPv4, subnet4cidr, "")
	if err != nil {
		return nil, err
	}

	cleanup6, err := setupOneNat(iptables.ProtocolIPv6, subnet6cidr, "")
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

func setupOneNat(proto iptables.Protocol, netmask string, servicesIP string) (func() error, error) {
	ipt, err := iptables.New(iptables.IPFamily(proto), iptables.Timeout(5))
	if err != nil {
		return nil, err
	}

	// TODO interface?
	err = ipt.AppendUnique("nat", "POSTROUTING", "-s", netmask, "!", "-d", netmask, "-j", "MASQUERADE")
	if err != nil {
		return nil, err
	}

	if servicesIP != "" {
		err = ipt.AppendUnique("filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", servicesIP, "-j", "REJECT", "--reject-with", "tcp-reset")
		if err != nil {
			return nil, err
		}
	}

	return func() error {
		err = ipt.DeleteIfExists("nat", "POSTROUTING", "-s", netmask, "!", "-d", netmask, "-j", "MASQUERADE")
		if err != nil {
			return err
		}

		if servicesIP != "" {
			err = ipt.DeleteIfExists("filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", servicesIP, "-j", "REJECT", "--reject-with", "tcp-reset")
			if err != nil {
				return err
			}
		}

		return nil
	}, nil
}

func getDefaultAddress4() net.IP {
	conn, err := net.Dial("udp", "1.0.0.1:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To4()
}

func getDefaultAddress6() net.IP {
	conn, err := net.Dial("udp", "[2606:4700:4700::1001]:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To16()
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
