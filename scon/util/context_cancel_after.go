package util

import (
	"context"
	"time"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

type CancelAfter struct {
	cancel context.CancelFunc

	mu         syncx.Mutex
	cancelTime time.Time
	lastTimer  *time.Timer
}

func NewTimedCancelFunc(cancel context.CancelFunc) *CancelAfter {
	return &CancelAfter{
		cancel: cancel,
	}
}

func (t *CancelAfter) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cancelTime = time.Now()
	t.cancel()
}

func (t *CancelAfter) CancelAt(when time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cancelTime.IsZero() || t.cancelTime.After(when) {
		t.cancelTime = when
		if t.lastTimer != nil {
			if !t.lastTimer.Stop() {
				// callback already fired
				return
			}
		}

		t.lastTimer = time.AfterFunc(time.Until(when), t.cancel)
	}
}

func (t *CancelAfter) CancelAfter(d time.Duration) {
	t.CancelAt(time.Now().Add(d))
}
