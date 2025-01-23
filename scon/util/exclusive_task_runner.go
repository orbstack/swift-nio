package util

import (
	"sync"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

type task struct {
	wg  sync.WaitGroup
	val any
	err error
}

type ExclusiveTaskRunner[I comparable] struct {
	mu    syncx.Mutex
	tasks map[I]*task
}

func NewExclusiveTaskRunner[I comparable]() *ExclusiveTaskRunner[I] {
	return &ExclusiveTaskRunner[I]{
		tasks: make(map[I]*task),
	}
}

func (m *ExclusiveTaskRunner[I]) TryBegin(id I) (shouldReturn bool, returnValue any, returnErr error) {
	m.mu.Lock()
	if t, ok := m.tasks[id]; ok {
		m.mu.Unlock()
		t.wg.Wait()
		// safe to read t.val and t.err; they are only written under m.mu and before task waitgroup is done
		return true, t.val, t.err
	} else {
		t := &task{}
		t.wg.Add(1)
		m.tasks[id] = t
		m.mu.Unlock()

		return false, nil, nil
	}
}

func (m *ExclusiveTaskRunner[I]) MarkComplete(id I, val any, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		delete(m.tasks, id)
		// safe because we're under m.mu and before t.wg.Done()
		t.val = val
		t.err = err
		t.wg.Done()
	}
}

func (m *ExclusiveTaskRunner[I]) Run(id I, fn func() (any, error)) (any, error) {
	shouldReturn, returnValue, returnErr := m.TryBegin(id)
	if shouldReturn {
		return returnValue, returnErr
	}

	val, err := fn()
	m.MarkComplete(id, val, err)
	return val, err
}
