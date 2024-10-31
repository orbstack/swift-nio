//go:build deadlock

package syncx

import (
	"github.com/sasha-s/go-deadlock"
)

// don't stop the application when running with -tags deadlock
// go-deadlock immediately detects recursive locking scon even though there is none
// that makes -tags deadlock unusable without this
func init() {
	deadlock.Opts.OnPotentialDeadlock = func() {}
}

type Mutex = deadlock.Mutex
type RWMutex = deadlock.RWMutex
