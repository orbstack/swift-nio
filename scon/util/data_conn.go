package util

import (
	"net"
	"time"
)

type DataConn struct {
	Conn net.Conn
	Data any
}

// check interface conformance
var _ net.Conn = &DataConn{}

func NewDataConn(conn net.Conn, data any) *DataConn {
	return &DataConn{
		Conn: conn,
		Data: data,
	}
}

func (c *DataConn) Read(b []byte) (n int, err error) {
	return c.Conn.Read(b)
}

func (c *DataConn) Write(b []byte) (n int, err error) {
	return c.Conn.Write(b)
}

func (c *DataConn) Close() error {
	return c.Conn.Close()
}

func (c *DataConn) LocalAddr() net.Addr {
	return c.Conn.LocalAddr()
}

func (c *DataConn) RemoteAddr() net.Addr {
	return c.Conn.RemoteAddr()
}

func (c *DataConn) SetDeadline(t time.Time) error {
	return c.Conn.SetDeadline(t)
}

func (c *DataConn) SetReadDeadline(t time.Time) error {
	return c.Conn.SetReadDeadline(t)
}

func (c *DataConn) SetWriteDeadline(t time.Time) error {
	return c.Conn.SetWriteDeadline(t)
}
