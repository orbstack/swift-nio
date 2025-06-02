package syncx

import (
	"time"
)

// leading debounce: call immediately, then ignore calls for duration. DO not reset the timer
type LeadingFuncDebounce struct {
	_ noCopy

	duration time.Duration

	mu Mutex

	timer           *time.Timer
	nextCallAt      time.Time
	nextCallPending bool

	// separate mutex to avoid blocking callers if func is slow
	funcMu Mutex
	fn     func()
}

func NewLeadingFuncDebounce(duration time.Duration, fn func()) *LeadingFuncDebounce {
	return &LeadingFuncDebounce{
		fn:       fn,
		duration: duration,
	}
}

func (d *LeadingFuncDebounce) Call() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.nextCallAt.IsZero() {
		go d.invoke()
		d.nextCallAt = time.Now().Add(d.duration)
	} else if !d.nextCallPending {
		timerDuration := time.Until(d.nextCallAt)
		if d.timer == nil {
			d.timer = time.AfterFunc(timerDuration, func() {
				d.mu.Lock()
				d.nextCallAt = time.Time{}
				d.nextCallPending = false
				d.mu.Unlock()

				d.invoke()
			})
		} else {
			d.timer.Reset(timerDuration)
		}

		d.nextCallPending = true
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
