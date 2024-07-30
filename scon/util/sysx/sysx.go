package sysx

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func PollFd(fd int, events int16) error {
	for {
		fds := [1]unix.PollFd{
			{
				Fd:     int32(fd),
				Events: events,
			},
		}
		n, err := unix.Poll(fds[:], -1)
		if err != nil {
			if err == unix.EINTR {
				continue
			} else {
				return err
			}
		}
		if n >= 1 {
			return nil
		}
	}
}

func Swapoff(path string) error {
	// null-terminated string
	cStr, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}

	_, _, errno := unix.Syscall(unix.SYS_SWAPOFF, uintptr(unsafe.Pointer(cStr)), 0, 0)
	if errno != 0 {
		return errno
	}

	return nil
}
