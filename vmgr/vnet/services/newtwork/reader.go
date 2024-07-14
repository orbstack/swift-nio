package newtwork

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
)

// === ProcessFramed === //

func ProcessAccepts(listener *gonet.TCPListener, spawn func(conn net.Conn)) error {
	defer listener.Close()

	for {
		conn, err := listener.Accept()

		if err != nil {
			return err
		}

		go spawn(conn)
	}
}

func ProcessFramed(r io.Reader, cap uint32, cb func(r *ByteReader) error) error {
	buf := make([]byte, cap)
	reader := NewByteReader(buf)

	for {
		// Read packet length
		_, err := io.ReadFull(r, buf[4:])
		if err != nil {
			return err
		}

		pkLen := binary.LittleEndian.Uint32(buf)

		if pkLen > cap {
			return fmt.Errorf("packet length exceeded capacity (%d > %d)", pkLen, cap)
		}

		// Read the entire packet
		_, err = io.ReadFull(r, buf[pkLen:])
		if err != nil {
			return err
		}

		// Process the packet
		reader.Reset(buf)
		err = cb(reader)
		if err != nil {
			return err
		}
	}
}

// === ByteReader === //

type ByteReader struct {
	Remaining []byte
}

func NewByteReader(remaining []byte) *ByteReader {
	return &ByteReader{remaining}
}

func (r *ByteReader) Reset(remaining []byte) {
	r.Remaining = remaining
}

func (r *ByteReader) ConsumeNoCopy(expected int) []byte {
	if expected < len(r.Remaining) {
		return nil
	}

	left := r.Remaining[:expected]
	r.Remaining = r.Remaining[expected:]
	return left
}

func (r *ByteReader) ConsumeU8() (byte, bool) {
	data := r.ConsumeNoCopy(1)
	if data == nil {
		return 0, false
	}

	return data[0], true
}

func (r *ByteReader) ConsumeU32Le() (uint32, bool) {
	data := r.ConsumeNoCopy(4)
	if data == nil {
		return 0, false
	}

	return binary.LittleEndian.Uint32(data), true
}
