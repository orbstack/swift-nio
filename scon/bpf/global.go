package bpf

import (
	"errors"
	"io"
)

type GlobalBpfManager struct {
	closers []io.Closer
}

func NewGlobalBpfManager() (*GlobalBpfManager, error) {
	return &GlobalBpfManager{}, nil
}

func (g *GlobalBpfManager) Close() error {
	var errs []error
	for _, c := range g.closers {
		err := c.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (g *GlobalBpfManager) Load() error {
	return nil
}
