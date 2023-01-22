package syncx

import (
	"sync"
	"time"
)

type FuncDebounce struct {
	f        func()
	mu       sync.Mutex
	timer    *time.Timer
	duration time.Duration
}

func NewFuncDebounce(duration time.Duration, f func()) FuncDebounce {
	return FuncDebounce{
		f:        f,
		duration: duration,
	}
}

func (d *FuncDebounce) Call() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.duration, d.f)
}
