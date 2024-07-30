package util

import (
	"os"
	"syscall"
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
