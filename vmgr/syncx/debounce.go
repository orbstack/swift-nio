package syncx

import (
	"sync"
	"time"
)

// leading debounce: call immediately, then ignore calls for duration. DO not reset the timer
type LeadingFuncDebounce struct {
	mu       sync.Mutex
	fn       func()
	duration time.Duration
	timer    *time.Timer
	pending  bool
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
		d.fn()
		d.timer = time.AfterFunc(d.duration, func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			// reset
			d.timer = nil
			// call if needed
			if d.pending {
				d.fn()
				d.pending = false
			}
		})
	} else {
		d.pending = true
	}
}
