//go:build !deadlock

package syncx

import "sync"

type Mutex = sync.Mutex
type RWMutex = sync.RWMutex
