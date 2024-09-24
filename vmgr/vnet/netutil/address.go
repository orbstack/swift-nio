package netutil

import (
	"net"
	"net/netip"

	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"gvisor.dev/gvisor/pkg/tcpip"
)

var (
	vnetSubnet4IpNet *net.IPNet
	vnetSubnet6IpNet *net.IPNet

	vnetHostNatIP4 net.IP
	vnetHostNatIP6 net.IP
)

func init() {
	var err error
	_, vnetSubnet4IpNet, err = net.ParseCIDR(netconf.VnetSubnet4CIDR)
	if err != nil {
		panic(err)
	}

	_, vnetSubnet6IpNet, err = net.ParseCIDR(netconf.VnetSubnet6CIDR)
	if err != nil {
		panic(err)
	}

	vnetHostNatIP4 = net.ParseIP(netconf.VnetHostNatIP4)
	vnetHostNatIP6 = net.ParseIP(netconf.VnetHostNatIP6)
}

// IPv4 or IPv6, properly sized
func ParseTcpipAddress(ip string) tcpip.Address {
	addr := net.ParseIP(ip)
	if addr4 := addr.To4(); addr4 != nil {
		return tcpip.AddrFrom4Slice(addr4)
	} else {
		return tcpip.AddrFrom16Slice(addr.To16())
	}
}

func GetDefaultAddress4() net.IP {
	conn, err := net.Dial("udp", "8.8.4.4:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To4()
}

func GetDefaultAddress6() net.IP {
	conn, err := net.Dial("udp", "[2606:4700:4700::1001]:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To16()
}

func ShouldForward(addr tcpip.Address) bool {
	ip := net.IP(addr.AsSlice())
	if ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}

	// IPv4 broadcast (DHCP)
	if ip.Equal(net.IPv4bcast) {
		return false
	}

	// vnet: block all except host NAT IPs (which do go through forward path)
	if vnetSubnet4IpNet.Contains(ip) {
		return vnetHostNatIP4.Equal(ip)
	}

	if vnetSubnet6IpNet.Contains(ip) {
		return vnetHostNatIP6.Equal(ip)
	}

	return true
}

func AddrFromNetip(addr netip.Addr) tcpip.Address {
	if addr.Is4() {
		return tcpip.AddrFrom4(addr.As4())
	} else if addr.Is6() {
		return tcpip.AddrFrom16(addr.As16())
	} else {
		return tcpip.Address{}
	}
}

func NetipFromAddr(addr tcpip.Address) netip.Addr {
	if addr.Len() == 4 {
		return netip.AddrFrom4(addr.As4())
	} else if addr.Len() == 16 {
		return netip.AddrFrom16(addr.As16())
	} else {
		return netip.Addr{}
	}
}
