package main

import (
	"errors"
	"time"

	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
)

const freezeCountClosed = ^int32(0)

var (
	ErrFreezerClosed = errors.New("freezer closed")
)

type frozenTarget int

const (
	frozenTargetFrozenIfIdle frozenTarget = iota
	frozenTargetFrozenForced
	frozenTargetUnfrozen
)

type Freezer struct {
	mu        syncx.Mutex
	container *Container
	debounce  *syncx.FuncDebounce

	// must not take c.mu
	Predicate func() (bool, error)

	useCount    int32
	freezeCount int32
	frozen      bool
}

func NewContainerFreezer(c *Container, debouncePeriod time.Duration) *Freezer {
	f := &Freezer{
		container: c,
		// start with 1 ref
		useCount:    1,
		freezeCount: 0,
	}

	f.debounce = syncx.NewFuncDebounce(debouncePeriod, func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.updateStateLocked()
	})

	return f
}

func (f *Freezer) BeginUse() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed() {
		return
	}

	f.useCount++
	if verboseDebug {
		logrus.WithField("count", f.useCount).Debug("freezer inc use")
	}

	f.updateStateLocked()
}

func (f *Freezer) EndUse() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed() {
		return
	}

	f.useCount--
	newCount := f.useCount
	if verboseDebug {
		logrus.WithField("count", newCount).Debug("freezer dec use")
	}
	if newCount == 0 {
		logrus.Debug("freezer last ref, freezing")
		f.debounce.Call()
	}
}

func (f *Freezer) BeginFreeze() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed() {
		return
	}

	f.freezeCount++
	if verboseDebug {
		logrus.WithField("count", f.freezeCount).Debug("freezer inc freeze")
	}

	f.updateStateLocked()
}

func (f *Freezer) EndFreeze() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed() {
		return
	}

	f.freezeCount--
	if verboseDebug {
		logrus.WithField("count", f.freezeCount).Debug("freezer dec freeze")
	}

	f.updateStateLocked()
}

func (f *Freezer) closed() bool {
	return f.useCount == freezeCountClosed
}

func (f *Freezer) getTargetFrozenLocked() (frozenTarget, error) {
	// closed = always unfrozen
	if f.closed() {
		return frozenTargetUnfrozen, nil
	}

	// freezeCount > 0 = forced frozen
	if f.freezeCount > 0 {
		return frozenTargetFrozenForced, nil
	}

	// useCount > 0 = unfrozen
	if f.useCount > 0 {
		return frozenTargetUnfrozen, nil
	}

	// freezeCount == 0, useCount == 0 = frozen if idle
	return frozenTargetFrozenIfIdle, nil
}

func (f *Freezer) updateStateLocked() {
	f.debounce.Cancel()

	target, err := f.getTargetFrozenLocked()
	if err != nil {
		logrus.WithError(err).Error("failed to get target frozen state")
		return
	}

	switch target {
	case frozenTargetFrozenIfIdle:
		if !f.frozen {
			logrus.Debug("freezer target -> frozen if idle. checking predicate")
			if f.Predicate != nil {
				ok, err := f.Predicate()
				if err != nil {
					logrus.WithError(err).Error("failed to call predicate")
					return
				}

				if !ok {
					logrus.Debug("freeze blocked: predicate")
					return
				}
			}

			logrus.Debug("freezer target -> frozen if idle. freezing")
			err := f.container.freezeInternal()
			if err != nil {
				logrus.WithError(err).Error("failed to freeze container")
				return
			}

			f.frozen = true
		}

	case frozenTargetFrozenForced:
		if !f.frozen {
			logrus.Debug("freezer target -> frozen forced. freezing")
			err := f.container.freezeInternal()
			if err != nil {
				logrus.WithError(err).Error("failed to freeze container")
				return
			}

			f.frozen = true
		}

	case frozenTargetUnfrozen:
		if f.frozen {
			logrus.Debug("freezer target -> unfrozen. unfreezing")
			err := f.container.unfreezeInternal()
			if err != nil {
				logrus.WithError(err).Error("failed to unfreeze container")
				return
			}

			f.frozen = false
		}
	}
}

func (f *Freezer) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.useCount = freezeCountClosed
	f.debounce.Cancel()
}
