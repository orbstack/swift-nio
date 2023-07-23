package util

import (
	"context"
	"sync"
	"time"
)

func WithTimeout[T any](fn func() (T, error), timeout time.Duration) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var outputMu sync.Mutex
	var result T
	err := context.DeadlineExceeded
	go func() {
		outRes, outErr := fn()

		outputMu.Lock()
		result = outRes
		err = outErr
		outputMu.Unlock()

		cancel()
	}()

	<-ctx.Done()

	outputMu.Lock()
	defer outputMu.Unlock()
	return result, err
}
