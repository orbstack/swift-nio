package netutil

import (
	"net"
	"strings"

	"gvisor.dev/gvisor/pkg/tcpip"
)

// IPv4 or IPv6, properly sized
func ParseTcpipAddress(ip string) tcpip.Address {
	addr := net.ParseIP(ip)
	if strings.ContainsRune(ip, ':') {
		return tcpip.Address(addr.To16())
	} else {
		return tcpip.Address(addr.To4())
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

func ShouldProxy(addr tcpip.Address) bool {
	ip := net.IP(addr)
	if ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}

	// IPv4 broadcast (DHCP)
	if ip.Equal(net.IPv4bcast) {
		return false
	}

	return true
}
