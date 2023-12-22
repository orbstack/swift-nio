//go:build !linux

package mdns

import (
	"net"
)

func setSoRcvmark(conn *net.UDPConn) error {
	return nil
}

func parseMark(oob []byte) uint32 {
	return 0
}
