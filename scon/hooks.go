package main

type ContainerHooks interface {
	Config(*Container, containerConfigMethods) error
	PreStart(*Container) error
	MakeFreezer(*Container, *ContainerRuntimeState) (*Freezer, error)
	PostStart(*Container, *ContainerRuntimeState) error
	OnStop(*Container) error
	PostStop(*Container) error
}

type NoopHooks struct{}

func (h *NoopHooks) Config(*Container, containerConfigMethods) error {
	return nil
}

func (h *NoopHooks) PreStart(*Container) error {
	return nil
}

func (h *NoopHooks) MakeFreezer(*Container, *ContainerRuntimeState) (*Freezer, error) {
	return nil, nil
}

func (h *NoopHooks) PostStart(*Container, *ContainerRuntimeState) error {
	return nil
}

func (h *NoopHooks) OnStop(*Container) error {
	return nil
}

func (h *NoopHooks) PostStop(*Container) error {
	return nil
}
