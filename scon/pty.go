package main

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

func (c *Container) OpenPty() (pty, tty *os.File, err error) {
	// TOOD cache this
	ptsDir, err := c.lxc.DevptsFd()
	if err != nil {
		return
	}
	// works as keepalive
	defer ptsDir.Close()

	// cloexec safe: O_CLOEXEC
	ptyFd, err := unix.Openat(int(ptsDir.Fd()), "ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	pty = os.NewFile(uintptr(ptyFd), "/dev/ptmx")
	defer runtime.KeepAlive(pty)

	// unlock
	err = unix.IoctlSetPointerInt(int(pty.Fd()), unix.TIOCSPTLCK, 0)
	if err != nil {
		pty.Close()
		return
	}

	// open tty peer
	ttyFd, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(pty.Fd()), unix.TIOCGPTPEER, uintptr(os.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC))
	if errno != 0 {
		err = errno
		pty.Close()
		return
	}
	tty = os.NewFile(ttyFd, "/dev/pts/tty")

	// caller fixes ownership
	return
}
