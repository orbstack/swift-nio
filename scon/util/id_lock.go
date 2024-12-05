package util

import (
	"fmt"
	"sync/atomic"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

type waiterMutex struct {
	mu      syncx.Mutex
	waiters atomic.Int32
}

type IDMutex[T comparable] struct {
	globalMu syncx.Mutex
	mutexes  map[T]*waiterMutex
}

func NewIDMutex[T comparable]() *IDMutex[T] {
	return &IDMutex[T]{
		mutexes: make(map[T]*waiterMutex),
	}
}

func (m *IDMutex[T]) Lock(id T) {
	m.globalMu.Lock()

	if waiterMu, ok := m.mutexes[id]; ok {
		waiterMu.waiters.Add(1)
		m.globalMu.Unlock()

		waiterMu.mu.Lock()

		waiterMu.waiters.Add(-1)
	} else {
		waiterMu := &waiterMutex{}
		waiterMu.mu.Lock()

		m.mutexes[id] = waiterMu

		m.globalMu.Unlock()
	}
}

func (m *IDMutex[T]) Unlock(id T) {
	m.globalMu.Lock()
	defer m.globalMu.Unlock()

	if waiterMu, ok := m.mutexes[id]; ok {
		waiterMu.mu.Unlock()
		if waiterMu.waiters.Load() == 0 {
			delete(m.mutexes, id)
		}
	} else {
		panic(fmt.Sprintf("no waiter mutex found for id: %v", id))
	}
}
