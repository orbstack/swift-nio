package util

import (
	"sync"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

type ExclusiveIdempotentTaskTracker[T comparable] struct {
	mu    syncx.Mutex
	tasks map[T]*sync.WaitGroup
}

func NewExclusiveIdempotentTaskTracker[T comparable]() *ExclusiveIdempotentTaskTracker[T] {
	return &ExclusiveIdempotentTaskTracker[T]{
		tasks: make(map[T]*sync.WaitGroup),
	}
}

func (m *ExclusiveIdempotentTaskTracker[T]) TryBegin(id T) (taskAlreadyComplete bool) {
	m.mu.Lock()
	if wg, ok := m.tasks[id]; ok {
		m.mu.Unlock()
		wg.Wait()
		return true
	} else {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		m.tasks[id] = wg
		m.mu.Unlock()
		return false
	}
}

func (m *ExclusiveIdempotentTaskTracker[T]) MarkComplete(id T) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if wg, ok := m.tasks[id]; ok {
		delete(m.tasks, id)
		wg.Done()
	}
}
