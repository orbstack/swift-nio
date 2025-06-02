package syncx

import (
	"sync"
	"time"
)

type FuncDebounce struct {
	_ noCopy

	// fewer allocations to embed both Mutex and Cond as values, and have Cond reference mu
	mu           Mutex
	timer        *time.Timer
	duration     time.Duration
	pendingCalls int32
	pendingCond  sync.Cond

	fn   func()
	fnMu Mutex
}

// expected behavior: fn() can't run concurrently, but shouldn't block timer kick
// returns pointer because it's self-referential (cond references mu)
func NewFuncDebounce(duration time.Duration, fn func()) *FuncDebounce {
	d := &FuncDebounce{
		fn:       fn,
		duration: duration,
	}
	d.pendingCond.L = &d.mu
	return d
}

func (d *FuncDebounce) Call() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer == nil {
		d.timer = time.AfterFunc(d.duration, d.timerCallback)
		d.pendingCalls++
	} else {
		d.timer.Reset(d.duration)
	}
}

func (d *FuncDebounce) timerCallback() {
	d.fnMu.Lock()
	d.fn()
	d.fnMu.Unlock()

	d.mu.Lock()
	d.pendingCalls--
	if d.pendingCalls == 0 {
		d.pendingCond.Broadcast()
	}
	d.mu.Unlock()
}

func (d *FuncDebounce) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		if d.timer.Stop() {
			d.pendingCalls--
		}
	}
}

func (d *FuncDebounce) CancelAndWait() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		// if Stop()=true, timer was successfully canceled so there will be one less future run
		// if Stop()=false, timer has already fired (in which case there will be a future completed run that we can wait for), or timer was already stopped (in which case there will be the same amount of future runs)
		if d.timer.Stop() {
			d.pendingCalls--
		}
	}

	for d.pendingCalls > 0 {
		d.pendingCond.Wait()
	}
}

// TODO: test this
/*
func (d *FuncDebounce) CallNow() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// cancel existing run
	if d.timer != nil {
		if d.timer.Stop() {
			d.pendingCalls--
		}
	}

	d.fnMu.Lock()
	d.fn()
	d.fnMu.Unlock()

	// notify a pending CancelAndWait() that we're done, if there are no more pending calls
	if d.pendingCalls == 0 {
		d.pendingCond.Broadcast()
	}
}
*/
