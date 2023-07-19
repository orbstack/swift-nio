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
func GetNetnsCookieFd(fd int) (uint64, error) {
	return unix.GetsockoptUint64(int(fd), unix.SOL_SOCKET, unix.SO_NETNS_COOKIE)
}

func GetNetnsCookie() (uint64, error) {
	// cloexec safe
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd)

	return GetNetnsCookieFd(fd)
}
