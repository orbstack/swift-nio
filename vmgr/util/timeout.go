package util

import (
	"errors"
	"time"
)

var (
	ErrFnTimeout = errors.New("func timeout")
)

func WithTimeout[T any](fn func() (T, error), timeout time.Duration) (T, error) {
	// wil be GC'd once both are done
	done := make(chan struct{}, 1)

	var result T
	var err error
	go func() {
		result, err = fn()
		done <- struct{}{}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return result, err
	case <-timer.C:
		return result, ErrFnTimeout
	}
}
