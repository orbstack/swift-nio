package sysns

import (
	"fmt"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

// need to lock main thread so that it's not possible for mountns switching to take place on it
// otherwise, /proc/self/mountinfo could be wrong if WithMountNs happens to run on the main thread
// easier than converting everything to /proc/thread-self (esp. libraries)
func init() {
	runtime.LockOSThread()
}

var getHostMountnsFd = sync.OnceValue(func() int {
	fd, err := unix.Open("/proc/thread-self/ns/mnt", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		panic(err)
	}
	return fd
})

func WithMountNs[T any](newNsFd int, fn func() (T, error)) (T, error) {
	var zero T

	// lock this thread, and never unlock it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// open current mount ns
	hostMountnsFd := getHostMountnsFd()

	// unshare mount namespace, as it's per-process
	// this makes a new process with everything but mount ns shared
	// implies CLONE_FS: current working dir, etc.
	err := unix.Unshare(unix.CLONE_NEWNS)
	if err != nil {
		return zero, fmt.Errorf("unshare: %w", err)
	}
	// revert to host mount ns
	defer unix.Chdir("/")
	defer unix.Setns(hostMountnsFd, unix.CLONE_NEWNS)

	// now we have a different mount ns from original process.
	// switch to target mount ns
	err = unix.Setns(newNsFd, unix.CLONE_NEWNS)
	if err != nil {
		return zero, err
	}
	// chdir needed to prevent relative symlink vuln
	err = unix.Chdir("/")
	if err != nil {
		return zero, err
	}

	return fn()
}

func WithMountNs1(newNsFd int, fn func() error) error {
	_, err := WithMountNs(newNsFd, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}
