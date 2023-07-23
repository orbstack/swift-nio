package simplerate

import (
	"sync"
	"time"
)

// a simple N-events in X-time sliding window limiter
type Limiter struct {
	mu    sync.Mutex
	slots []time.Time

	nEvents int
	period  time.Duration
}

// NewLimiter creates a new limiter with the given period
func NewLimiter(nEvents int, period time.Duration) *Limiter {
	return &Limiter{
		slots: make([]time.Time, 0, nEvents),

		nEvents: nEvents,
		period:  period,
	}
}

// Allow returns true if an event is allowed at time now
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	// remove old events
	for len(l.slots) > 0 && now.Sub(l.slots[0]) > l.period {
		l.slots = l.slots[1:]
	}

	// if we have space, add an event
	if len(l.slots) < l.nEvents {
		l.slots = append(l.slots, now)
		return true
	}

	// otherwise, we're full
	return false
}
