package syncx

import "sync"

type CondBool struct {
	_ noCopy

	// fewer allocations to embed both Mutex and Cond as values, and have Cond reference mu
	mu    Mutex
	cond  sync.Cond
	value bool
}

func NewCondBool() *CondBool {
	c := &CondBool{}
	c.cond.L = &c.mu
	return c
}

func (c *CondBool) Set(value bool) {
	c.mu.Lock()
	c.value = value
	c.mu.Unlock()
	c.cond.Broadcast()
}

func (c *CondBool) Get() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Wait blocks until the value is true. If the value is already true, it
// returns immediately.
func (c *CondBool) Wait() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for !c.value {
		c.cond.Wait()
	}
}

type CondValue[T comparable] struct {
	_ noCopy

	mu        Mutex
	cond      sync.Cond
	value     T
	expectNot T
}

func NewCondValue[T comparable](initial T, expectNot T) *CondValue[T] {
	c := &CondValue[T]{
		value:     initial,
		expectNot: expectNot,
	}
	c.cond.L = &c.mu
	return c
}

func (c *CondValue[T]) Set(value T) {
	c.mu.Lock()
	c.value = value
	c.mu.Unlock()
	c.cond.Broadcast()
}

func (c *CondValue[T]) Get() T {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// Wait blocks until the value is true. If the value is already true, it
// returns immediately.
func (c *CondValue[T]) Wait() T {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.value == c.expectNot {
		c.cond.Wait()
	}
	return c.value
}
