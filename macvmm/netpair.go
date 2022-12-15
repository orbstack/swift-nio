package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func makeUnixDgramPair() (file0 *os.File, file1 *os.File, err error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[0], unix.SOL_SOCKET, unix.SO_SNDBUF, dgramSockBuf)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[0], unix.SOL_SOCKET, unix.SO_RCVBUF, dgramSockBuf*4)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[1], unix.SOL_SOCKET, unix.SO_SNDBUF, dgramSockBuf)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[1], unix.SOL_SOCKET, unix.SO_RCVBUF, dgramSockBuf*4)
	if err != nil {
		return
	}

	// this works fine, but makes little difference
	err = unix.SetNonblock(fds[0], true)
	if err != nil {
		return
	}
	err = unix.SetNonblock(fds[1], true)
	if err != nil {
		return
	}

	file0 = os.NewFile(uintptr(fds[0]), "socketpair0")
	file1 = os.NewFile(uintptr(fds[1]), "socketpair1")

	return
}
