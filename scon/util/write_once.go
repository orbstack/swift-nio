package util

import "sync/atomic"

type WriteOnce[T any] struct {
	val atomic.Pointer[T]
}

func (w *WriteOnce[T]) Store(val T) bool {
	return w.val.CompareAndSwap(nil, &val)
}

func (w *WriteOnce[T]) Load() T {
	return *w.val.Load()
}
