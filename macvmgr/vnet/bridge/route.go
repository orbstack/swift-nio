package bridge

import (
	"net"
)

func isRouteCorrect(hostIP net.IP) (bool, error) {
	// check src addr for route
	// simpler, faster, less error-prone than looking up route table
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   hostIP,
		Port: 65535,
	})
	if err != nil {
		return false, err
	}
	defer conn.Close()

	srcAddr := conn.LocalAddr().(*net.UDPAddr).IP
	// we're good if src == dest ip (interface could be localhost, but it doesn't matter)
	if srcAddr.Equal(hostIP) {
		//fmt.Println("dial", hostIP, "got", srcAddr, "= OK")
		return true, nil
	}

	// for IPv6, we're also good if it's a link local src addr
	if srcAddr.To4() == nil && srcAddr.IsLinkLocalUnicast() {
		//fmt.Println("dial", hostIP, "got", srcAddr, "= OK")
		return true, nil
	}

	//fmt.Println("dial", hostIP, "got", srcAddr, "= XXXXXXXXXX")
	return false, nil
}
