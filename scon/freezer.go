package main

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

var (
	ErrFreezerClosed = errors.New("freezer closed")
)

type Freezer struct {
	container *Container
	count     int
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
		count:     1, // start with a ref
	}
	debounce := syncx.NewFuncDebounce(debouncePeriod, func() {
		err := f.tryFreeze()
		if err != nil {
			logrus.WithError(err).Error("failed to update freezer state")
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
	f.count++
	logrus.WithField("count", f.count).Debug("freezer inc ref")

	if f.count == 1 {
		logrus.Debug("freezer first ref, unfreezing")
		err := f.doUnfreezeLocked()
		if err != nil {
			logrus.WithError(err).Error("failed to unfreeze on ref")
		}
	}
}

func (f *Freezer) decRefCLocked() {
	debounce := f.debounce.Load()
	if debounce == nil {
		return
	}

	f.count--
	logrus.WithField("count", f.count).Debug("freezer dec ref")
	if f.count == 0 {
		logrus.Debug("freezer last ref, freezing")
		debounce.Call()
	}

	if f.count < 0 {
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
