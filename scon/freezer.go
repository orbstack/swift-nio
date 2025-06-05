package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
)

const freezeCountClosed = ^int32(0)

var (
	ErrFreezerClosed = errors.New("freezer closed")
)

type Freezer struct {
	mu        syncx.Mutex
	container *Container
	debounce  *syncx.FuncDebounce
	// must not take c.mu
	predicate func() (bool, error)

	count  int32
	frozen bool
}

func NewContainerFreezer(c *Container, debouncePeriod time.Duration, predicate func() (bool, error)) *Freezer {
	f := &Freezer{
		container: c,
		// start with 1 ref
		count:     1,
		predicate: predicate,
	}
	f.debounce = syncx.NewFuncDebounce(debouncePeriod, func() {
		err := f.tryFreeze()
		if err != nil {
			logrus.WithError(err).Error("failed to update cfref state")
		}
	})

	return f
}

func (f *Freezer) IncRef() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.count == freezeCountClosed {
		return
	}

	f.debounce.Cancel()
	f.count++
	newCount := f.count
	if verboseDebug {
		logrus.WithField("count", newCount).Debug("freezer inc ref")
	}

	if newCount == 1 && f.frozen {
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

	if f.count == freezeCountClosed {
		return
	}

	f.count--
	newCount := f.count
	if verboseDebug {
		logrus.WithField("count", newCount).Debug("freezer dec ref")
	}
	if newCount == 0 {
		logrus.Debug("freezer last ref, freezing")
		f.debounce.Call()
	}

	if newCount < 0 {
		logrus.Error("freezer refcount < 0")
	}
}

func (f *Freezer) tryFreeze() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.frozen {
		logrus.Debug("freeze blocked: already frozen")
		return nil
	}
	if f.count == freezeCountClosed {
		logrus.Debug("freeze blocked: closed")
		return nil
	}
	if f.count > 0 {
		logrus.Debug("freeze blocked: refs >= 1")
		return nil
	}

	if f.predicate != nil {
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
	err := f.container.freezeInternal()
	if err != nil {
		return err
	}

	f.frozen = true
	return nil
}

func (f *Freezer) doUnfreezeLocked() error {
	return f.container.unfreezeInternal()
}

func (f *Freezer) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.count = freezeCountClosed
	f.debounce.Cancel()

	if f.frozen {
		err := f.doUnfreezeLocked()
		if err != nil {
			logrus.WithError(err).Error("failed to unfreeze cfref on close")
		}
	}
}
