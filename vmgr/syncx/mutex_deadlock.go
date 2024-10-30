//go:build deadlock

package syncx

import (
	"github.com/sasha-s/go-deadlock"
)

func init() {
	deadlock.Opts.OnPotentialDeadlock = func() {}
}

type Mutex = deadlock.Mutex
type RWMutex = deadlock.RWMutex
