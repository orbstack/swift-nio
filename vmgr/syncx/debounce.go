package syncx

import (
	"sync"
	"time"
)

/*
 * 4 possible states:
 * - quiescent: no pending calls, no in-progress calls
 * - pending: no in-progress calls, 1 pending call
 * - in-progress: 1 in-progress call
 * - pending+in-progress: 1 pending call, 1 in-progress call
 *
 * possible transitions:
 * - quiescent -> pending (via Call)
 * - pending -> quiescent (via Cancel)
 * - pending -> in-progress (via timerCallback start)
 * - in-progress -> quiescent (via timerCallback finish)
 * - in-progress -> pending+in-progress (via Call)
 * - pending+in-progress -> in-progress (via Cancel* or timerCallback finish)
 */
type FuncDebounce struct {
	_ noCopy

	// config
	duration time.Duration
	fn       func()

	// fewer allocations to embed both Mutex and Cond as values, and have Cond reference mu
	mu    Mutex
	timer *time.Timer
	// signaled when debouncer *might* be entering a quiescent state (no pending calls AND no in-progress calls)
	// waiter is responsible for verifying the state
	maybeQuiescentCond sync.Cond

	callInProgress bool
	callPending    bool
}

// expected behavior: fn() can't run concurrently, but shouldn't block timer kick
// returns pointer because it's self-referential (cond references mu)
func NewFuncDebounce(duration time.Duration, fn func()) *FuncDebounce {
	d := &FuncDebounce{
		fn:       fn,
		duration: duration,
	}
	d.maybeQuiescentCond.L = &d.mu
	return d
}

// Call() guarantees that fn() will be called at least once in the future as long as Cancel() or CancelAndWait() is not called after it.
// It does NOT guarantee that at least `duration` will pass before fn() is called. For example, a proceeding CallNow() will cause it to run immediately.
func (d *FuncDebounce) Call() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer == nil {
		d.timer = time.AfterFunc(d.duration, d.timerCallback)
	} else {
		d.timer.Reset(d.duration)
	}
	d.callPending = true
}

func (d *FuncDebounce) timerCallback() {
	d.mu.Lock()
	for d.callInProgress {
		// wait for the existing call to finish before starting a new one
		d.maybeQuiescentCond.Wait()
	}
	// if no calls are pending (i.e. timer fired then canceled), bail
	if !d.callPending {
		d.mu.Unlock()
		return
	}
	// convert pending call to in-progress call
	d.callPending = false
	d.callInProgress = true
	d.mu.Unlock()

	// this is exclusive due to the wait for maybeQuiescentCond
	d.fn()

	d.mu.Lock()
	// end the current in-progress call
	d.callInProgress = false
	// signal maybe-quiescent state (we always cause a transition: callInProgress=false)
	d.maybeQuiescentCond.Broadcast()
	d.mu.Unlock()
}

func (d *FuncDebounce) Cancel() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	if d.callPending {
		d.callPending = false
		// now quiescent if callInProgress=false
		d.maybeQuiescentCond.Broadcast()
	}
}

func (d *FuncDebounce) CancelAndWait() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.timer != nil {
		d.timer.Stop()
	}

	// no need to broadcast maybeQuiescentCond here: we're the only one checking callPending under it
	d.callPending = false

	// we have to check d.callPending because someone could call Call() after we've canceled the last pending call but before the last in-progress call has finished
	// in that case we should wait for the newly-pending call, otherwise it violates the guarantee that Call() will call fn() at least once in the future before Cancel*()
	for d.callInProgress || d.callPending {
		d.maybeQuiescentCond.Wait()
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
		d.maybeQuiescentCond.Broadcast()
	}
}
*/
