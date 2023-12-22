//go:build linux

package mdns

import (
	"encoding/binary"
	"net"

	"golang.org/x/sys/unix"
)

func setSoRcvmark(conn *net.UDPConn) error {
	// set SO_RCVMARK on Linux
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var err2 error
	err = rawConn.Control(func(fd uintptr) {
		err2 = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVMARK, 1)
	})
	if err != nil {
		return err
	}
	if err2 != nil {
		return err2
	}

	return nil
}

func parseMark(oob []byte) uint32 {
	var mark uint32
	cmsgs, err := unix.ParseSocketControlMessage(oob)
	if err == nil {
		for _, cmsg := range cmsgs {
			if cmsg.Header.Level == unix.SOL_SOCKET && cmsg.Header.Type == unix.SO_RCVMARK {
				mark = binary.LittleEndian.Uint32(cmsg.Data)
				break
			}
		}
	}

	return mark
}
