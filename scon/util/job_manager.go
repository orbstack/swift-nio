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
		ctx:    ctx,
		cancel: cancel,
	}
}

func (m *EntityJobManager) Run(job func(ctx context.Context) error) error {
	m.globalWg.Add(1)
	defer m.globalWg.Done()

	return job(m.ctx)
}

func (m *EntityJobManager) RunContext(ctx context.Context, job func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return m.Run(func(managerCtx context.Context) error {
		go func() {
			select {
			case <-managerCtx.Done():
			case <-ctx.Done():
			}
			cancel()
		}()

		return job(ctx)
	})
}

func (m *EntityJobManager) Close() {
	m.cancel()
	m.globalWg.Wait()
}
