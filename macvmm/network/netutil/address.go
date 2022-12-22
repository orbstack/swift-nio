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
	conn, err := net.Dial("udp", "1.1.1.1:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To4()
}

func GetDefaultAddress6() net.IP {
	conn, err := net.Dial("udp", "[2606:4700:4700::1111]:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To16()
}
