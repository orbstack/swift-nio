package vnet

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/vnet/sockets"
	"golang.org/x/sys/unix"
)

func NewUnixgramPair() (file0 *os.File, fd1 int, err error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return
	}

	// cloexec
	unix.CloseOnExec(fds[0])
	unix.CloseOnExec(fds[1])
	unix.SetNonblock(fds[0], true)
	unix.SetNonblock(fds[1], true)

	err = sockets.SetLargeBuffers(fds[0])
	if err != nil {
		unix.Close(fds[0])
		unix.Close(fds[1])
		return
	}
	err = sockets.SetLargeBuffers(fds[1])
	if err != nil {
		unix.Close(fds[0])
		unix.Close(fds[1])
		return
	}

	file0 = os.NewFile(uintptr(fds[0]), "socketpair0")
	fd1 = fds[1]

	return
}
