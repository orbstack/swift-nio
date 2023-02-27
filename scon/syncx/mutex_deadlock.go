//go:build deadlock

package syncx

import (
	"github.com/sasha-s/go-deadlock"
)

type Mutex = deadlock.Mutex
type RWMutex = deadlock.RWMutex
