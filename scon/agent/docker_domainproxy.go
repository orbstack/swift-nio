package agent

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/orbstack/macvirt/scon/domainproxy"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

type DockerAddDomainproxyArgs struct {
	Ip  netip.Addr
	Val net.IP
}

func (a *AgentServer) DockerAddDomainproxy(args DockerAddDomainproxyArgs, reply *None) error {
	logrus.WithFields(logrus.Fields{"ip": args.Ip, "val": args.Val}).Debug("emmie | DockerAddDomainproxy")
	var err error
	if args.Ip.Is4() {
		err = nft.Run("add", "element", "inet", "orbstack", "domainproxy4", fmt.Sprintf("{ %v : %v }", args.Ip, args.Val))
	}
	if args.Ip.Is6() {
		err = nft.Run("add", "element", "inet", "orbstack", "domainproxy6", fmt.Sprintf("{ %v : %v }", args.Ip, args.Val))
	}
	if err != nil {
		return err
	}

	return nil
}

func (a *AgentServer) DockerRemoveDomainproxy(ip netip.Addr, reply *None) error {
	logrus.WithFields(logrus.Fields{"ip": ip}).Debug("emmie | DockerRemoveDomainproxy")
	var err error
	if ip.Is4() {
		err = nft.Run("delete", "element", "inet", "orbstack", "domainproxy4", fmt.Sprintf("{ %v }", ip))
	}
	if ip.Is6() {
		err = nft.Run("delete", "element", "inet", "orbstack", "domainproxy6", fmt.Sprintf("{ %v }", ip))
	}
	if err != nil {
		return err
	}

	return nil
}

func replaceIpBase(ip netip.Addr, base netip.Prefix) netip.Addr {
	ipSlice := ip.AsSlice()
	baseSlice := base.Masked().Addr().AsSlice()
	bits := base.Bits()
	for i, _ := range ipSlice {
		ipSlice[i] &= ^uint8(0) >> min(bits, 8)
		ipSlice[i] |= baseSlice[i]
		bits -= 8
		if bits <= 0 {
			break
		}
	}
	// we got ipSlice from .AsSlice so it must be either 4 or 16 bits in length, so we expect ok to be true
	ip, ok := netip.AddrFromSlice(ipSlice)
	if !ok {
		panic("unexpected length of slice from netip.Addr.AsSlice")
	}

	return ip
}

func (d *DockerAgent) startDomaintproxy() error {
	domainproxySubnet4Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet4Cidr)
	domainproxySubnet6Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet6Cidr)

	getMark := func(upstream domainproxytypes.DomainproxyUpstream) int {
		return netconf.DockerFwmarkTproxyOutbound
	}

	proxy, err := domainproxy.NewDomaintproxy(d.host, d.scon.GetProxyUpstream, getMark)
	if err != nil {
		return fmt.Errorf("create tls domainproxy: %w", err)
	}
	d.domaintproxy = proxy

	err = proxy.Start(netconf.VnetTlsProxyIP4, netconf.VnetTlsProxyIP6, domainproxySubnet4Prefix, domainproxySubnet6Prefix)
	if err != nil {
		return err
	}

	logrus.Debug("started docker tls domaintproxy")

	return nil
}
