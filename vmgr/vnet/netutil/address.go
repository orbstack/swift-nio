package netutil

import (
	"net"
	"net/netip"

	"gvisor.dev/gvisor/pkg/tcpip"
)

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
