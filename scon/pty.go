package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func (c *Container) OpenPty() (pty, tty *os.File, err error) {
	ptsDir, err := c.lxc.DevptsFd()
	if err != nil {
		return
	}
	defer ptsDir.Close()

	// cloexec safe: O_CLOEXEC
	ptyFd, err := unix.Openat(int(ptsDir.Fd()), "ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	pty = os.NewFile(uintptr(ptyFd), "/dev/ptmx")

	// unlock
	val := 0
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(ptyFd), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&val)))
	if errno != 0 {
		err = errno
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
