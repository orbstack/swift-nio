package bridge

import (
	"net"
)

func isRouteCorrect(hostIP net.IP) (bool, error) {
	// check src addr for route
	// simpler, faster, less error-prone than looking up route table
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{
		IP:   hostIP,
		Port: 65535,
	})
	if err != nil {
		return false, err
	}
	defer conn.Close()

	srcAddr := conn.LocalAddr().(*net.UDPAddr).IP
	return srcAddr.Equal(hostIP), nil
}
