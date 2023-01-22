package syncx

import "sync"

type CondBool struct {
	cond  *sync.Cond
	value bool
}

func NewCondBool() CondBool {
	return CondBool{
		cond: sync.NewCond(&sync.Mutex{}),
	}
}

func (c *CondBool) Set(value bool) {
	c.cond.L.Lock()
	c.value = value
	c.cond.L.Unlock()
	c.cond.Broadcast()
}

func (c *CondBool) Get() bool {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()
	return c.value
}

// Wait blocks until the value is true. If the value is already true, it
// returns immediately.
func (c *CondBool) Wait() {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()
	for !c.value {
		c.cond.Wait()
	}
}

type CondValue[T comparable] struct {
	cond      *sync.Cond
	value     T
	expectNot T
}

func NewCondValue[T comparable](initial T, expectNot T) CondValue[T] {
	return CondValue[T]{
		cond: sync.NewCond(&sync.Mutex{}),
	}
}

func (c *CondValue[T]) Set(value T) {
	c.cond.L.Lock()
	c.value = value
	c.cond.L.Unlock()
	c.cond.Broadcast()
}

func (c *CondValue[T]) Get() T {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()
	return c.value
}

// Wait blocks until the value is true. If the value is already true, it
// returns immediately.
func (c *CondValue[T]) Wait() T {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()
	for c.value == c.expectNot {
		c.cond.Wait()
	}
	return c.value
}
