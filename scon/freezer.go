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
	container *Container
	count     atomic.Int32
	predicate func() (bool, error)
	debounce  atomic.Pointer[syncx.FuncDebounce]
}

// freezer does not have its own lock
// instead, it uses the container's lock
// otherwise we run into inconsistent lock order issues
func NewContainerFreezer(c *Container, debouncePeriod time.Duration, predicate func() (bool, error)) *Freezer {
	f := &Freezer{
		container: c,
		predicate: predicate,
	}
	// start with a ref
	f.count.Store(1)
	debounce := syncx.NewFuncDebounce(debouncePeriod, func() {
		err := f.tryFreeze()
		if err != nil {
			logrus.WithError(err).Error("failed to update cfref state")
		}
	})
	f.debounce.Store(&debounce)

	return f
}

func (f *Freezer) incRefCLocked() {
	debounce := f.debounce.Load()
	if debounce == nil {
		return
	}

	debounce.Cancel()
	newCount := f.count.Add(1)
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

func (f *Freezer) IncRef() {
	f.container.mu.Lock()
	defer f.container.mu.Unlock()

	f.incRefCLocked()
}

func (f *Freezer) decRefCLocked() {
	debounce := f.debounce.Load()
	if debounce == nil {
		return
	}

	newCount := f.count.Add(-1)
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

func (f *Freezer) DecRef() {
	f.container.mu.Lock()
	defer f.container.mu.Unlock()

	f.decRefCLocked()
}

func (f *Freezer) tryFreeze() error {
	// take container lock
	f.container.mu.Lock()
	defer f.container.mu.Unlock()

	count := f.count.Load()
	if count > 0 {
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

	// one more sanity check: if there's another ref now, unfreeze
	if f.count.Load() > 0 {
		logrus.Warn("cfref count increased in critical section, undoing")
		err := f.doUnfreezeLocked()
		if err != nil {
			logrus.WithError(err).Error("failed to thaw cfref on ref inconsistency")
		}
	}

	return nil
}

func (f *Freezer) doUnfreezeLocked() error {
	c := f.container
	if !c.IsFrozen() {
		return nil
	}

	err := c.Unfreeze()
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
