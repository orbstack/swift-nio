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
	if err != nil {
		return zero, err
	}
	defer unix.Setns(int(currentNs.Fd()), unix.CLONE_NEWNET)

	return fn()
}
