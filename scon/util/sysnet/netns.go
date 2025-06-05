package sysnet

import (
	"os"
	"runtime"
	"sync"

	"github.com/orbstack/macvirt/scon/util/dirfs"
	"golang.org/x/sys/unix"
)

// opened once, never closed
var GetProcessNetnsFd = sync.OnceValue(func() int {
	// can't use PIDFD_GET_PID_NAMESPACE: can't ioctl on PIDFD_SELF so we'd have to pidfd_open
	fd, err := unix.Open("/proc/thread-self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		panic(err)
	}
	return fd
})

// NOT SAFE for pollable fds (i.e. from pidfd_open)
func WithNetnsFile[T any](newNsF *os.File, fn func() (T, error)) (T, error) {
	defer runtime.KeepAlive(newNsF)
	return WithNetnsFd(int(newNsF.Fd()), fn)
}

func WithNetnsFd[T any](newNsFd int, fn func() (T, error)) (T, error) {
	var zero T

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// get current ns
	hostNetnsFd := GetProcessNetnsFd()

	// set ns
	err := unix.Setns(newNsFd, unix.CLONE_NEWNET)
	if err != nil {
		return zero, err
	}
	defer unix.Setns(hostNetnsFd, unix.CLONE_NEWNET)

	return fn()
}

func WithNetnsProcDirfs[T any](procDirfs *dirfs.FS, fn func() (T, error)) (T, error) {
	nsFd, err := procDirfs.OpenFd("ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		var zero T
		return zero, err
	}
	defer unix.Close(nsFd)

	return WithNetnsFd(nsFd, fn)
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
