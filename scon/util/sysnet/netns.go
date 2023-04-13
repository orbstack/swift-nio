package sysnet

import (
	"os"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

func WithNetns[T any](newNsF *os.File, fn func() (T, error)) (T, error) {
	var zero T

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// get current ns
	currentNs, err := os.Open("/proc/self/task/" + strconv.Itoa(unix.Gettid()) + "/ns/net")
	if err != nil {
		return zero, err
	}
	defer currentNs.Close()

	// set ns
	err = unix.Setns(int(newNsF.Fd()), unix.CLONE_NEWNET)
	runtime.KeepAlive(newNsF)
	if err != nil {
		return zero, err
	}
	defer unix.Setns(int(currentNs.Fd()), unix.CLONE_NEWNET)

	return fn()
}

// used for eBPF
func GetNetnsCookie(socketFile *os.File) (uint64, error) {
	rawConn, err := socketFile.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cookie uint64
	var err2 error
	err = rawConn.Control(func(fd uintptr) {
		cookie, err2 = unix.GetsockoptUint64(int(fd), unix.SOL_SOCKET, unix.SO_NETNS_COOKIE)
	})
	if err != nil {
		return 0, err
	}
	if err2 != nil {
		return 0, err2
	}

	return cookie, nil
}
