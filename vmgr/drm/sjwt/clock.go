package sjwt

import (
	"sync"
	"time"
)

const (
	clockSyncInterval = 12 * time.Hour
)

var currentClock = sync.OnceValue(newSyncedClock)

type ClockSource interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now()
}

// TODO: ntp and hybrid
type ntpClock struct{}

func (ntpClock) Now() time.Time {
	panic("not implemented")
}

type HybridClock struct {
	sys          ClockSource
	ref          ClockSource
	lastSyncedAt time.Time
	offset       time.Duration
}

func (h *HybridClock) Now() time.Time {
	// TODO: offset must use timex monotonic if we do NTP sync in the future
	if h.sys.Now().Sub(h.lastSyncedAt) > clockSyncInterval {
		h.Sync()
	}

	return h.sys.Now().Add(h.offset)
}

func (h *HybridClock) Sync() {
	h.offset = h.ref.Now().Sub(h.sys.Now())
	h.lastSyncedAt = h.sys.Now()
}

func newSyncedClock() ClockSource {
	clock := &HybridClock{
		sys: SystemClock{},
		// TODO
		//ref: ntpClock{},
		ref: SystemClock{},
	}

	go clock.Sync()
	return clock
}
