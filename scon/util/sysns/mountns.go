package sysns

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// need to lock main thread so that it's not possible for mountns switching to take place on it
// otherwise, /proc/self/mountinfo could be wrong if WithMountNs happens to run on the main thread
// easier than converting everything to /proc/thread-self (esp. libraries)
func init() {
	runtime.LockOSThread()
}

type result[T any] struct {
	val T
	err error
}

func WithMountNs[T any](newNsFd int, fn func() (T, error)) (T, error) {
	// LockOSThread is per-goroutine, so make a new temp goroutine
	resultCh := make(chan result[T])
	go func() {
		// wrap func for return value
		ret, err := func() (T, error) {
			var zero T

			// lock this thread, and never unlock it.
			runtime.LockOSThread()

			// unshare mount namespace, as it's per-process
			// this makes a new process with everything but mount ns shared
			// implies CLONE_FS: current working dir, etc.
			err := unix.Unshare(unix.CLONE_NEWNS)
			if err != nil {
				return zero, fmt.Errorf("unshare: %w", err)
			}

			// now we have a different mount ns from original process.
			// switch to target mount ns
			err = unix.Setns(newNsFd, unix.CLONE_NEWNS)
			if err != nil {
				return zero, err
			}
			// chdir and chroot not needed

			// run the func. when it's done, the thread will exit because we never called UnlockOSThread
			return fn()
		}()
		resultCh <- result[T]{ret, err}
	}()

	result := <-resultCh
	return result.val, result.err
}

func WithMountNs1(newNsFd int, fn func() error) error {
	_, err := WithMountNs(newNsFd, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}
