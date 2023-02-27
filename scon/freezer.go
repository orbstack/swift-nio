package main

import (
	"errors"
	"time"

	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

type Freezer struct {
	container *Container
	mu        syncx.Mutex
	count     int
	predicate func() (bool, error)
	debounce  syncx.FuncDebounce
}

func NewContainerFreezer(c *Container, debounce time.Duration, predicate func() (bool, error)) *Freezer {
	f := &Freezer{
		container: c,
		predicate: predicate,
		count:     1, // start with a ref
	}
	f.debounce = syncx.NewFuncDebounce(debounce, func() {
		err := f.tryFreeze()
		if err != nil {
			logrus.WithError(err).Error("failed to update freezer state")
		}
	})

	return f
}

func (f *Freezer) IncRef() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.debounce.Cancel()
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

func (f *Freezer) DecRef() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.count--
	logrus.WithField("count", f.count).Debug("freezer dec ref")
	if f.count == 0 {
		logrus.Debug("freezer last ref, freezing")
		f.debounce.Call()
	}

	if f.count < 0 {
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

	c := f.container
	if c == nil {
		return errors.New("freezer is closed")
	}

	if c.IsFrozen() {
		logrus.Debug("freeze blocked: already frozen")
		return nil
	}

	if f.predicate != nil {
		// release lock for the predicate - it could call UseAgent
		f.mu.Unlock()
		ok, err := f.predicate()
		f.mu.Lock()
		if err != nil {
			return err
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
	if c == nil {
		return errors.New("freezer is closed")
	}

	if !c.IsFrozen() {
		return nil
	}

	err := c.Unfreeze()
	if err != nil && !errors.Is(err, lxc.ErrNotFrozen) {
		return err
	}

	return nil
}

func (f *Freezer) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.debounce.Cancel()
	f.container = nil
}
