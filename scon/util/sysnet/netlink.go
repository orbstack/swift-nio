package sysnet

import (
	"os"

	"golang.org/x/sys/unix"
)

func OpenDiagNetlink() (*os.File, error) {
	// open netlink socket
	// cloexec safe
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_INET_DIAG)
	if err != nil {
		return nil, err
	}

	// set nonblock (persists over SCM_RIGHTS transfer)
	err = unix.SetNonblock(fd, true)
	if err != nil {
		return nil, err
	}

	return os.NewFile(uintptr(fd), "[netlink]"), nil
}
