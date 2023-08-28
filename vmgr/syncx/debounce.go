package syncx

import (
	"sync"
	"sync/atomic"
	"time"
)

// leading debounce: call immediately, then ignore calls for duration. DO not reset the timer
type LeadingFuncDebounce struct {
	// avoid blocking callers if func is slow
	mu       sync.Mutex
	funcMu   sync.Mutex
	fn       func()
	duration time.Duration
	timer    *time.Timer
	pending  atomic.Bool
}

func NewLeadingFuncDebounce(fn func(), duration time.Duration) *LeadingFuncDebounce {
	return &LeadingFuncDebounce{
		fn:       fn,
		duration: duration,
	}
}

func (d *LeadingFuncDebounce) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer == nil {
		go d.invoke()
		d.timer = time.AfterFunc(d.duration, func() {
			d.mu.Lock()
			// reset
			d.timer = nil
			d.mu.Unlock()
			// call if needed
			if d.pending.CompareAndSwap(true, false) {
				d.invoke()
			}
		})
	} else {
		d.pending.Store(true)
	}
}

func (d *LeadingFuncDebounce) invoke() {
	d.funcMu.Lock()
	defer d.funcMu.Unlock()
	d.fn()
}

func (d *LeadingFuncDebounce) CallNow() {
	go d.invoke()
}
