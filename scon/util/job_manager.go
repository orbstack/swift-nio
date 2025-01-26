package util

import (
	"context"
	"sync"
)

type EntityJobManager struct {
	globalWg sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
}

func NewEntityJobManager(ctx context.Context) *EntityJobManager {
	ctx, cancel := context.WithCancel(ctx)
	return &EntityJobManager{
		globalWg: sync.WaitGroup{},
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (m *EntityJobManager) Context() context.Context {
	return m.ctx
}

func (m *EntityJobManager) Run(job func(ctx context.Context) error) error {
	var thisWg sync.WaitGroup
	thisWg.Add(1)
	m.globalWg.Add(1)

	var err error
	go func() {
		defer thisWg.Done()
		defer m.globalWg.Done()

		err = job(m.ctx)
	}()

	thisWg.Wait()
	return err
}

func (m *EntityJobManager) Close() {
	m.cancel()
	m.globalWg.Wait()
}
