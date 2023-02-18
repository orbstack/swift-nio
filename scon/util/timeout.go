package util

import (
	"errors"
	"time"
)

func WithTimeout[T any](fn func() (T, error), timeout time.Duration) (T, error) {
	// wil be GC'd once both are done
	done := make(chan struct{})

	var result T
	var err error
	go func() {
		result, err = fn()
		select {
		case done <- struct{}{}:
		default:
		}
	}()

	select {
	case <-done:
		return result, err
	case <-time.After(timeout):
		return result, errors.New("timeout")
	}
}
