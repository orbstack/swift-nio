package main

import (
	"encoding/binary"
	"fmt"
	"net"
)

type FpllClient struct {
	conn net.Conn
}

func NewFpllClient() (*FpllClient, error) {
	conn, err := net.Dial("unix", "/run/fpll.sock")
	if err != nil {
		return nil, err
	}

	return &FpllClient{conn: conn}, nil
}

func (c *FpllClient) NotifyDeleteSubdir(subdir string) error {
	// write 4-byte host endian length
	err := binary.Write(c.conn, binary.LittleEndian, uint32(len(subdir)))
	if err != nil {
		return err
	}

	// write subdir bytes
	_, err = c.conn.Write([]byte(subdir))
	if err != nil {
		return err
	}

	// wait for response
	var buf [4]byte
	_, err = c.conn.Read(buf[:])
	if err != nil {
		return err
	}

	// decode int
	resp := int32(binary.LittleEndian.Uint32(buf[:]))
	if resp != 0 {
		return fmt.Errorf("fpll: %d", resp)
	}

	return nil
}

func (c *FpllClient) Close() error {
	return c.conn.Close()
}
