package sysnet

import (
	"os"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

var getHostNetnsFd = sync.OnceValue(func() int {
	fd, err := unix.Open("/proc/thread-self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		panic(err)
	}
	return fd
})

func WithNetns[T any](newNsF *os.File, fn func() (T, error)) (T, error) {
	var zero T

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// get current ns
	hostNetnsFd := getHostNetnsFd()

	// set ns
	err := unix.Setns(int(newNsF.Fd()), unix.CLONE_NEWNET)
	runtime.KeepAlive(newNsF)
	if err != nil {
		return zero, err
	}
	defer unix.Setns(hostNetnsFd, unix.CLONE_NEWNET)

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
