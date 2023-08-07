package util

import (
	"os"
	"sync"

	"github.com/sirupsen/logrus"
)

var (
	oomBlockRefcount = 0
	oomBlockMu       sync.Mutex

	oomScoreAdjDefault = "0"
	oomScoreAdjScon    = "-950"

	// mitigate risk of 512k tcp buffers using too much memory: not too aggressive oom adj
	OomScoreAdjCriticalGuest = "-500"
)

func setOomScoreAdj(score string) {
	err := os.WriteFile("/proc/self/oom_score_adj", []byte(score), 0644)
	if err != nil {
		logrus.WithError(err).Error("failed to set oom_score_adj")
	}
}

func WithDefaultOom1(fn func() error) error {
	// prelude
	oomBlockMu.Lock()
	oomBlockRefcount++
	if oomBlockRefcount == 1 {
		setOomScoreAdj(oomScoreAdjDefault)
	}
	oomBlockMu.Unlock()

	// postlude
	defer func() {
		oomBlockMu.Lock()
		oomBlockRefcount--
		if oomBlockRefcount == 0 {
			setOomScoreAdj(oomScoreAdjScon)
		}
		oomBlockMu.Unlock()
	}()

	return fn()
}

func WithDefaultOom2[T any](fn func() (T, error)) (T, error) {
	// prelude
	oomBlockMu.Lock()
	oomBlockRefcount++
	if oomBlockRefcount == 1 {
		setOomScoreAdj(oomScoreAdjDefault)
	}
	oomBlockMu.Unlock()

	// postlude
	defer func() {
		oomBlockMu.Lock()
		oomBlockRefcount--
		if oomBlockRefcount == 0 {
			setOomScoreAdj(oomScoreAdjScon)
		}
		oomBlockMu.Unlock()
	}()

	return fn()
}
