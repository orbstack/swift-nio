package main

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/lxc/go-lxc"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/sirupsen/logrus"
)

var (
	ErrFreezerClosed = errors.New("freezer closed")
)

type Freezer struct {
	mu        syncx.Mutex
	container *Container
	count     int
	predicate func() (bool, error)
	debounce  atomic.Pointer[syncx.FuncDebounce]
}

func NewContainerFreezer(c *Container, debouncePeriod time.Duration, predicate func() (bool, error)) *Freezer {
	f := &Freezer{
		container: c,
		// start with 1 ref
		count:     1,
		predicate: predicate,
	}
	debounce := syncx.NewFuncDebounce(debouncePeriod, func() {
		err := f.tryFreeze()
		if err != nil {
			logrus.WithError(err).Error("failed to update cfref state")
		}
	})
	f.debounce.Store(&debounce)

	return f
}

func (f *Freezer) IncRef() {
	f.mu.Lock()
	defer f.mu.Unlock()

	debounce := f.debounce.Load()
	if debounce == nil {
		return
	}

	debounce.Cancel()
	f.count++
	newCount := f.count
	if verboseDebug {
		logrus.WithField("count", newCount).Debug("freezer inc ref")
	}

	if newCount == 1 {
		logrus.Debug("freezer first ref, unfreezing")
		err := f.doUnfreezeLocked()
		if err != nil {
			logrus.WithError(err).Error("failed to thaw cfref on ref")
		}
	}
}

func (f *Freezer) DecRef() {
	f.mu.Lock()
	defer f.mu.Unlock()

	debounce := f.debounce.Load()
	if debounce == nil {
		return
	}

	f.count--
	newCount := f.count
	if verboseDebug {
		logrus.WithField("count", newCount).Debug("freezer dec ref")
	}
	if newCount == 0 {
		logrus.Debug("freezer last ref, freezing")
		debounce.Call()
	}

	if newCount < 0 {
		logrus.Error("freezer refcount < 0")
	}
}

func (f *Freezer) tryFreeze() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.count > 0 {
		logrus.Debug("freeze blocked: refs >= 1")
		return nil
	}

	// in case debounce was scheduled before closed
	if f.debounce.Load() == nil {
		return nil
	}

	c := f.container
	if c.IsFrozen() {
		logrus.Debug("freeze blocked: already frozen")
		return nil
	}

	if f.predicate != nil {
		// release lock for the predicate - it could call UseAgent
		ok, err := f.predicate()
		if err != nil {
			return fmt.Errorf("call predicate: %w", err)
		}

		if !ok {
			logrus.Debug("freeze blocked: predicate")
			return nil
		}
	}

	logrus.Debug("freezing")
	err := c.Freeze()
	if err != nil && !errors.Is(err, lxc.ErrAlreadyFrozen) {
		return err
	}

	return nil
}

func (f *Freezer) doUnfreezeLocked() error {
	if !f.container.IsFrozen() {
		return nil
	}

	err := f.container.Unfreeze()
	if err != nil && !errors.Is(err, lxc.ErrNotFrozen) {
		return err
	}

	return nil
}

// close must be lock-free, or we'll deadlock on stop + tryFreeze predicate (which keeps the lock held)
func (f *Freezer) Close() {
	debounce := f.debounce.Swap(nil)
	if debounce != nil {
		debounce.Cancel()
	}
}
