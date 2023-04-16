package fsnotify

import (
	"encoding/binary"
	"errors"
	"net"
)

const (
	npFlagCreate = 1 << iota
	npFlagModify
	npFlagStatAttr
	npFlagRemove

	krpcMsgNotifyproxyInject = 1

	linuxPathMax = 4096
)

type krpcHeader struct {
	Len uint32
	Typ uint32
}

type krpcNotifyproxyInject struct {
	Count uint64
}

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

func (c *KrpcClient) NotifyproxyInject(batch eventBatch) error {
	totalPathLen := 0
	for _, path := range batch.Paths {
		totalPathLen += len(path) + 1
	}

	// prepare buffer
	buf := make([]byte, 0, 8+8+len(batch.Descs)*8+totalPathLen)
	// write header
	buf = binary.LittleEndian.AppendUint32(buf, uint32(8+len(batch.Descs)*8+totalPathLen))
	buf = binary.LittleEndian.AppendUint32(buf, krpcMsgNotifyproxyInject)
	// write count
	buf = binary.LittleEndian.AppendUint64(buf, uint64(len(batch.Descs)))
	// write descs
	for _, desc := range batch.Descs {
		buf = binary.LittleEndian.AppendUint64(buf, desc)
	}
	// write paths
	for _, path := range batch.Paths {
		buf = append(buf, path...)
		buf = append(buf, 0)
	}

	// sanity check
	if len(buf) != cap(buf) {
		return errors.New("buffer length mismatch")
	}

	// send
	_, err := c.conn.Write(buf)
	return err
}
