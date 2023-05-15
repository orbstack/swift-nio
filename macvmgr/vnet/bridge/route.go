package bridge

import (
	"net"

	"github.com/orbstack/macvirt/macvmgr/vnet/netconf"
)

var (
	ipSconHostBridgeIP4 = net.ParseIP(netconf.SconHostBridgeIP4)
)

func IsMachineRouteCorrect() (bool, error) {
	// check src addr for route
	// simpler, faster, less error-prone than looking up route table
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{
		IP:   ipSconHostBridgeIP4,
		Port: 65535,
	})
	if err != nil {
		return false, err
	}
	defer conn.Close()

	srcAddr := conn.LocalAddr().(*net.UDPAddr).IP
	return srcAddr.Equal(ipSconHostBridgeIP4), nil
}
