package fsnotify

import (
	"errors"
	"net"
)

type KrpcClient struct {
	conn net.Conn
}

func NewKrpcClient(conn net.Conn) *KrpcClient {
	return &KrpcClient{
		conn: conn,
	}
}

func (c *KrpcClient) Close() error {
	return c.conn.Close()
}

func (c *KrpcClient) WriteRaw(buf []byte) error {
	n, err := c.conn.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return errors.New("short write")
	}

	return nil
}
