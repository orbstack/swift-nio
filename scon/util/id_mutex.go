package util

import (
	"fmt"
	"sync/atomic"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

type waiterMutex struct {
	mu      syncx.Mutex
	waiters atomic.Uint32
}

type IDMutex[T comparable] struct {
	globalMu syncx.Mutex
	mutexes  map[T]*waiterMutex
}

func NewIDMutex[T comparable]() IDMutex[T] {
	return IDMutex[T]{
		mutexes: make(map[T]*waiterMutex),
	}
}

func (m *IDMutex[T]) Lock(id T) {
	// replicate the behavior of a global mutex
	//m.globalMu.Lock()
	//return

	m.globalMu.Lock()

	if waiterMu, ok := m.mutexes[id]; ok {
		waiterMu.waiters.Add(1)
		m.globalMu.Unlock()

		waiterMu.mu.Lock()

		// decrement
		waiterMu.waiters.Add(^uint32(0))
	} else {
		waiterMu := &waiterMutex{}
		waiterMu.mu.Lock()
		m.mutexes[id] = waiterMu

		m.globalMu.Unlock()
	}
}

func (m *IDMutex[T]) Unlock(id T) {
	// replicate the behavior of a global mutex
	//m.globalMu.Unlock()
	//return

	m.globalMu.Lock()
	defer m.globalMu.Unlock()

	waiterMu, ok := m.mutexes[id]
	if !ok {
		panic(fmt.Sprintf("no waiter mutex found for id: %v", id))
	}

	if waiterMu.waiters.Load() == 0 {
		delete(m.mutexes, id)
	}

	waiterMu.mu.Unlock()
}
