package udpfwd

import "net"

func NewUDPLocalProxy(conn net.PacketConn, isIPv6 bool, port uint16) (*UDPProxy, error) {
	dialer := func(clientAddr *net.UDPAddr) (net.Conn, error) {
		dialAddr := net.UDPAddr{
			Port: int(port),
		}
		if isIPv6 {
			dialAddr.IP = net.IPv6loopback
		} else {
			dialAddr.IP = net.IPv4(127, 0, 0, 1)
		}

		return net.DialUDP("udp", nil, &dialAddr)
	}

	return NewUDPProxy(conn, dialer)
}
