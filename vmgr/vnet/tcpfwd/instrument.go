package tcpfwd

import (
	"encoding/hex"
	"net"
	"time"

	"github.com/sirupsen/logrus"
)

type InstrumentedConn struct {
	tag string
	FullDuplexConn
}

func NewInstrumentedConn(tag string, c FullDuplexConn) FullDuplexConn {
	return &InstrumentedConn{
		tag:            tag,
		FullDuplexConn: c,
	}
}

// LocalAddr implements net.Conn.
func (c *InstrumentedConn) LocalAddr() net.Addr {
	ret := c.FullDuplexConn.LocalAddr()
	logrus.Debugf("%s.LocalAddr() = %+v", c.tag, ret)
	return ret
}

// Read implements net.Conn.
func (c *InstrumentedConn) Read(b []byte) (int, error) {
	n, err := c.FullDuplexConn.Read(b)
	hexData := hex.EncodeToString(b[:n])
	logrus.Debugf("%s.Read([%d]) = [%d; %s] (%v)", c.tag, len(b), n, hexData, err)
	return n, err
}

// RemoteAddr implements net.Conn.
func (c *InstrumentedConn) RemoteAddr() net.Addr {
	ret := c.FullDuplexConn.RemoteAddr()
	logrus.Debugf("%s.RemoteAddr() = %+v", c.tag, ret)
	return ret
}

// SetDeadline implements net.Conn.
func (c *InstrumentedConn) SetDeadline(t time.Time) error {
	ret := c.FullDuplexConn.SetDeadline(t)
	logrus.Debugf("%s.SetDeadline(%v) = %v", c.tag, t, ret)
	return ret
}

// SetReadDeadline implements net.Conn.
func (c *InstrumentedConn) SetReadDeadline(t time.Time) error {
	ret := c.FullDuplexConn.SetReadDeadline(t)
	logrus.Debugf("%s.SetReadDeadline(%v) = %v", c.tag, t, ret)
	return ret
}

// SetWriteDeadline implements net.Conn.
func (c *InstrumentedConn) SetWriteDeadline(t time.Time) error {
	ret := c.FullDuplexConn.SetWriteDeadline(t)
	logrus.Debugf("%s.SetWriteDeadline(%v) = %v", c.tag, t, ret)
	return ret
}

// Write implements net.Conn.
func (c *InstrumentedConn) Write(b []byte) (int, error) {
	n, err := c.FullDuplexConn.Write(b)
	hexData := hex.EncodeToString(b[:n])
	logrus.Debugf("%s.Write([%d; %s]) = %d (%v)", c.tag, len(b), hexData, n, err)
	return n, err
}

func (c *InstrumentedConn) Close() error {
	ret := c.FullDuplexConn.Close()
	logrus.Debugf("%s.Close() = %v", c.tag, ret)
	return ret
}

func (c *InstrumentedConn) CloseRead() error {
	ret := c.FullDuplexConn.CloseRead()
	logrus.Debugf("%s.CloseRead() = %v", c.tag, ret)
	return ret
}

func (c *InstrumentedConn) CloseWrite() error {
	ret := c.FullDuplexConn.CloseWrite()
	logrus.Debugf("%s.CloseWrite() = %v", c.tag, ret)
	return ret
}
