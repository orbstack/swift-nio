package util

import (
	"context"
	"time"
)

func WithTimeout[T any](fn func() (T, error), timeout time.Duration) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var result T
	err := context.DeadlineExceeded
	go func() {
		result, err = fn()
		cancel()
	}()

	<-ctx.Done()
	return result, err
}
