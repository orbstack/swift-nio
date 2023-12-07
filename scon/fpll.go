package main

import (
	"encoding/binary"
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

	return nil
}
