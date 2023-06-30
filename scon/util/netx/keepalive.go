package netx

import (
	"net"
	"time"
)

// golang default keepalive is 15 sec. disable to save CPU and avoid falsely triggering perf mgr activity
// https://github.com/golang/go/issues/48622
const (
	LongKeepalive = 3 * time.Minute
)

// for local conns only
func DisableKeepalive(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(false)
	}
}

// for external conns
func SetLongKeepalive(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlivePeriod(LongKeepalive)

		// if the peer is localhost, turn keepalive off entirely. no point
		if tcpConn.RemoteAddr().(*net.TCPAddr).IP.IsLoopback() {
			tcpConn.SetKeepAlive(false)
		}
	}
}
