package bpf

import (
	"fmt"

	"github.com/cilium/ebpf"
)

type GlobalBpfManager struct {
	sctl *sctlObjects
}

func NewGlobalBpfManager() (*GlobalBpfManager, error) {
	return &GlobalBpfManager{}, nil
}

func (g *GlobalBpfManager) Close() error {
	if g.sctl != nil {
		g.sctl.Close()
	}
	return nil
}

func (g *GlobalBpfManager) Load() error {
	// must load a new instance to set a different netns cookie in config map
	// maps are per-program instance
	// and this is an unpinned program (no ref in /sys/fs/bpf), so it'll be destroyed
	// when we close fds
	spec, err := loadSctl()
	if err != nil {
		return fmt.Errorf("load spec: %w", err)
	}

	objs := sctlObjects{}
	err = spec.LoadAndAssign(&objs, nil)
	if err != nil {
		return fmt.Errorf("load objs: %w", err)
	}

	g.sctl = &objs
	return nil
}

func (g *GlobalBpfManager) AttachSctl(b *ContainerBpfManager) error {
	return b.attachOneCg(ebpf.AttachCGroupSysctl, g.sctl.SysctlFilter)
}
