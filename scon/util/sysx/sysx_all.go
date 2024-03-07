package sysx

import "golang.org/x/sys/unix"

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
