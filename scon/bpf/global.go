package bpf

type GlobalBpfManager struct {
}

func NewGlobalBpfManager() (*GlobalBpfManager, error) {
	return &GlobalBpfManager{}, nil
}

func (g *GlobalBpfManager) Close() error {
	return nil
}

func (g *GlobalBpfManager) Load() error {
	return nil
}
