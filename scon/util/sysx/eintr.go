package sysx

import "golang.org/x/sys/unix"

func Eintr1(f func() error) error {
	for {
		err := f()
		if err == unix.EINTR {
			continue
		}
		return err
	}
}

func Eintr2[T any](f func() (T, error)) (T, error) {
	for {
		t, err := f()
		if err == unix.EINTR {
			continue
		}
		return t, err
	}
}

func Eintr3[T1 any, T2 any](f func() (T1, T2, error)) (T1, T2, error) {
	for {
		t1, t2, err := f()
		if err == unix.EINTR {
			continue
		}
		return t1, t2, err
	}
}
