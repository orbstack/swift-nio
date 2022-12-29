package sockets

import "golang.org/x/sys/unix"

const (
	dgramSockBuf = 512 * 1024
)

func SetLargeBuffers(fd int) error {
	err := unix.SetsockoptUint64(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, dgramSockBuf)
	if err != nil {
		return err
	}
	err = unix.SetsockoptUint64(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, dgramSockBuf*4)
	if err != nil {
		return err
	}
	return nil
}
