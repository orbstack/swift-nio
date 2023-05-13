package udpfwd

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/orbstack/macvirt/macvmgr/vnet/gonet"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func ListenUDP(addr string) (*net.UDPConn, bool, error) {
	addrPort, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, false, err
	}

	// disable udp46 for IPv4-only. we only do 4-6 for v6 listeners
	network := "udp4"
	if addrPort.Addr().Is6() {
		network = "udp"
	}

	if addrPort.Addr().IsLoopback() && addrPort.Port() < 1024 && addrPort.Port() != 0 {
		// Bypass privileged ports by listening on 0.0.0.0
		addr := net.IPv4zero
		if addrPort.Addr().Is6() {
			addr = net.IPv6zero
			// disable 4-in-6. if we intended to bind to localhost, then we only want v6.
			// there's no 4-in-6 for non-0000 addresses.
			network = "udp6"
		}

		l, err := net.ListenUDP(network, &net.UDPAddr{
			IP:   addr,
			Port: int(addrPort.Port()),
		})
		return l, true, err
	}

	l, err := net.ListenUDP(network, &net.UDPAddr{
		IP:   addrPort.Addr().AsSlice(),
		Port: int(addrPort.Port()),
	})
	return l, false, err
}

func StartUDPHostForward(s *stack.Stack, listenAddr, connectAddr4, connectAddr6 string) (*UDPProxy, error) {
	listener, requireLoopback, err := ListenUDP(listenAddr)
	if err != nil {
		return nil, err
	}

	connectUdpAddr4, err := net.ResolveUDPAddr("udp", connectAddr4)
	if err != nil {
		return nil, err
	}

	connectFullAddr4 := tcpip.FullAddress{
		Addr: tcpip.Address(connectUdpAddr4.IP),
		Port: uint16(connectUdpAddr4.Port),
	}

	connectUdpAddr6, err := net.ResolveUDPAddr("udp", connectAddr6)
	if err != nil {
		return nil, err
	}

	connectFullAddr6 := tcpip.FullAddress{
		Addr: tcpip.Address(connectUdpAddr6.IP),
		Port: uint16(connectUdpAddr6.Port),
	}

	dialer := func(remoteAddr *net.UDPAddr) (net.Conn, error) {
		// Check remote address if using 0.0.0.0 to bypass privileged ports for loopback
		if requireLoopback && !remoteAddr.IP.IsLoopback() {
			return nil, fmt.Errorf("rejecting connection from non-loopback address %s", remoteAddr)
		}

		proto := ipv4.ProtocolNumber
		connectAddr := connectFullAddr4
		// 4-in-6 means the listener is v6, so the other side must be v6
		is4in6 := remoteAddr.AddrPort().Addr().Is4In6()
		if is4in6 || remoteAddr.IP.To4() == nil {
			proto = ipv6.ProtocolNumber
			connectAddr = connectFullAddr6
		}

		return gonet.DialUDP(s, nil, &connectAddr, proto)
	}
	proxy, err := NewUDPProxy(listener, dialer, false)
	if err != nil {
		return nil, err
	}

	go proxy.Run(false)
	return proxy, nil
}
