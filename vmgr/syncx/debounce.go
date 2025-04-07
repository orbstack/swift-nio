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

	completionChan chan struct{}
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

	if d.timer == nil {
		d.timer = time.AfterFunc(d.duration, d.timerCallback)
	} else {
		d.timer.Reset(d.duration)
	}

	if d.completionChan == nil {
		d.completionChan = make(chan struct{})
	}
}

func (d *FuncDebounce) timerCallback() {
	d.fnMu.Lock()
	d.fn()
	d.fnMu.Unlock()

	d.mu.Lock()
	if d.completionChan != nil {
		close(d.completionChan)
		d.completionChan = nil
	}
	d.mu.Unlock()
}

func (d *FuncDebounce) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}
}

func (d *FuncDebounce) CancelAndWait() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	if d.completionChan != nil {
		<-d.completionChan
	}
}

func (d *FuncDebounce) CallNow() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	d.timerCallback()
}
