package syncx

import (
	"time"
)

type FuncDebounce struct {
	mu       Mutex
	timer    *time.Timer
	duration time.Duration

	fn   func()
	fnMu Mutex
}

// expected behavior: fn() can't run concurrently, but shouldn't block timer kick
func NewFuncDebounce(duration time.Duration, fn func()) FuncDebounce {
	return FuncDebounce{
		fn:       fn,
		duration: duration,
	}
}

func (d *FuncDebounce) Call() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timer = time.AfterFunc(d.duration, d.timerCallback)
}

func (d *FuncDebounce) timerCallback() {
	d.fnMu.Lock()
	defer d.fnMu.Unlock()

	d.fn()
}

func (d *FuncDebounce) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

func (d *FuncDebounce) CallNow() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}

	d.timerCallback()
}
