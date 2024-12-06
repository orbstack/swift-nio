package util

import (
	"io"
	"net"
	"time"
)

type OSConn interface {
	net.Conn

	// preserve TCP splice capabilities
	io.ReaderFrom
	io.WriterTo

	// and TCP half-open
	CloseWrite() error
	CloseRead() error
}

type DataConn[T any] struct {
	Conn OSConn
	Data T
}

// check interface conformance
var _ OSConn = &DataConn[any]{}

func NewDataConn[T any](conn OSConn, data T) *DataConn[T] {
	return &DataConn[T]{
		Conn: conn,
		Data: data,
	}
}

func (c *DataConn[T]) Read(b []byte) (n int, err error) {
	return c.Conn.Read(b)
}

func (c *DataConn[T]) Write(b []byte) (n int, err error) {
	return c.Conn.Write(b)
}

func (c *DataConn[T]) Close() error {
	return c.Conn.Close()
}

func (c *DataConn[T]) LocalAddr() net.Addr {
	return c.Conn.LocalAddr()
}

func (c *DataConn[T]) RemoteAddr() net.Addr {
	return c.Conn.RemoteAddr()
}

func (c *DataConn[T]) SetDeadline(t time.Time) error {
	return c.Conn.SetDeadline(t)
}

func (c *DataConn[T]) SetReadDeadline(t time.Time) error {
	return c.Conn.SetReadDeadline(t)
}

func (c *DataConn[T]) SetWriteDeadline(t time.Time) error {
	return c.Conn.SetWriteDeadline(t)
}

func (c *DataConn[T]) ReadFrom(r io.Reader) (n int64, err error) {
	return c.Conn.ReadFrom(r)
}

func (c *DataConn[T]) WriteTo(w io.Writer) (n int64, err error) {
	return c.Conn.WriteTo(w)
}

func (c *DataConn[T]) CloseWrite() error {
	return c.Conn.CloseWrite()
}

func (c *DataConn[T]) CloseRead() error {
	return c.Conn.CloseRead()
}
