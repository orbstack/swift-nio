package util

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// file.Fd() disables nonblock. this doesn't.
func GetFd(file *os.File) int {
	conn, err := file.SyscallConn()
	if err != nil {
		return -1
	}
	var fd int
	err = conn.Control(func(fdptr uintptr) {
		fd = int(fdptr)
	})
	if err != nil {
		return -1
	}
	return fd
}

func GetConnFd(conn syscall.Conn) int {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return -1
	}

	var fd int
	err = rawConn.Control(func(fdptr uintptr) {
		fd = int(fdptr)
	})
	if err != nil {
		return -1
	}
	return fd
}

// this takes a ref on f.pfd to prevent it from being closed
func UseFile(file *os.File, f func(int) error) error {
	rawConn, err := file.SyscallConn()
	if err != nil {
		return err
	}

	return UseRawConn(rawConn, f)
}

func UseFile1[T1 any](file *os.File, f func(int) (T1, error)) (T1, error) {
	rawConn, err := file.SyscallConn()
	if err != nil {
		var zero T1
		return zero, err
	}

	return UseRawConn1(rawConn, f)
}

func UseRawConn(rawConn syscall.RawConn, f func(int) error) error {
	var err2 error
	err := rawConn.Control(func(fd uintptr) {
		err2 = f(int(fd))
	})
	if err != nil {
		return err
	}

	return err2
}

func UseRawConn1[T1 any](rawConn syscall.RawConn, f func(int) (T1, error)) (T1, error) {
	var err2 error
	var ret1 T1
	err := rawConn.Control(func(fd uintptr) {
		ret1, err2 = f(int(fd))
	})
	if err != nil {
		return ret1, err
	}

	return ret1, err2
}

/*
read a small, simple text file with fewer syscalls than os.ReadFile. intended for sysfs, procfs, etc.

with os.ReadFile:
openat(AT_FDCWD, "/sys/fs/cgroup/scon/container/01GQQVF6C60000000000DOCKER.1ud3wuy/io.stat", O_RDONLY|O_CLOEXEC) = 179
fcntl(179, F_GETFL)         = 0x20000 (flags O_RDONLY|O_LARGEFILE)
fcntl(179, F_SETFL, O_RDONLY|O_NONBLOCK|O_LARGEFILE) = 0
epoll_ctl(4, EPOLL_CTL_ADD, 179, {events=EPOLLIN|EPOLLOUT|EPOLLRDHUP|EPOLLET, data=0xffff6621c7f800f3}) = 0
fstat(179, {st_mode=S_IFREG|0444, st_size=0, ...}) = 0
read(179, "254:16 rbytes=3568963584 wbytes="..., 512) = 157
read(179, "", 355)          = 0
epoll_ctl(4, EPOLL_CTL_DEL, 179, 0x400376b0b0) = 0
close(179)                  = 0
*/
func ReadFileFast(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)

	// stolen from io.ReadAll
	b := make([]byte, 0, 3072) // /proc/<pid>/net/dev is big
	for {
		n, err := unix.Read(fd, b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err != nil {
			return b, err
		}
		if n == 0 {
			return b, nil
		}

		if len(b) == cap(b) {
			// Add more capacity (let append pick how much).
			b = append(b, 0)[:len(b)]
		}
	}
}
