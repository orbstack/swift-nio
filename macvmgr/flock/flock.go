package flock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func Open(path string) (*os.File, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func Lock(file *os.File) error {
	flock := unix.Flock_t{
		Type:   unix.F_WRLCK,
		Whence: int16(unix.SEEK_SET),
		Start:  0,
		Len:    0,
	}

	// must use F_SETLK to get l_pid
	err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &flock)
	if err != nil {
		return err
	}

	return nil
}

func Unlock(file *os.File) error {
	flock := unix.Flock_t{
		Type:   unix.F_UNLCK,
		Whence: int16(unix.SEEK_SET),
		Start:  0,
		Len:    0,
	}

	err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &flock)
	if err != nil {
		return err
	}

	return nil
}

// flock is more atomic than pid file
func ReadPid(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}

		return 0, err
	}
	defer file.Close()

	flock := unix.Flock_t{
		Type:   unix.F_WRLCK,
		Whence: int16(unix.SEEK_SET),
		Start:  0,
		Len:    0,
	}

	err = unix.FcntlFlock(file.Fd(), unix.F_GETLK, &flock)
	if err != nil {
		return 0, err
	}

	if flock.Type == unix.F_UNLCK {
		return 0, nil
	} else {
		return int(flock.Pid), nil
	}
}
