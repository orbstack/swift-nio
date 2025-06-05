package sysx

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/orbstack/macvirt/scon/util"
	"golang.org/x/sys/unix"
)

// generic version
func PollFd(fd int, events int16) (int16, error) {
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
				return 0, err
			}
		}
		if n >= 1 {
			return fds[0].Revents, nil
		}
	}
}

// runtime poller (netpoll) version for reading
func RuntimePollFileRead(f *os.File) error {
	sc, err := f.SyscallConn()
	if err != nil {
		return err
	}

	// true = read done
	// false = keep waiting
	isFirst := true
	return sc.Read(func(fd uintptr) (done bool) {
		if isFirst {
			isFirst = false
			return false
		}

		return true
	})
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

func DupFile(f *os.File) (*os.File, error) {
	return util.UseFile1(f, func(fd int) (*os.File, error) {
		fd2, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3) // skip stdio
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd2), f.Name()), nil
	})
}
