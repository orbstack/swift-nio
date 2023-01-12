package udpfwd

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
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

	if addrPort.Addr().IsLoopback() && addrPort.Port() < 1024 {
		// Bypass privileged ports by listening on 0.0.0.0
		addr := net.IPv4zero
		if addrPort.Addr().Is6() {
			addr = net.IPv6zero
		}

		l, err := net.ListenUDP("udp", &net.UDPAddr{
			IP:   addr,
			Port: int(addrPort.Port()),
		})
		return l, true, err
	}

	l, err := net.ListenUDP("udp", &net.UDPAddr{
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

	dialer := func(fromAddr *net.UDPAddr) (net.Conn, error) {
		proto := ipv4.ProtocolNumber
		connectAddr := connectFullAddr4
		if fromAddr.IP.To4() == nil {
			proto = ipv6.ProtocolNumber
			connectAddr = connectFullAddr6
		}

		// Check remote address if using 0.0.0.0 to bypass privileged ports for loopback
		if requireLoopback && !fromAddr.IP.IsLoopback() {
			return nil, fmt.Errorf("rejecting connection from non-loopback address %s", fromAddr)
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
