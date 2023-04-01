package types

type ContainerState string

const (
	ContainerStateCreating ContainerState = "creating"
	ContainerStateStarting ContainerState = "starting"
	ContainerStateRunning  ContainerState = "running"
	ContainerStateStopping ContainerState = "stopping"
	ContainerStateStopped  ContainerState = "stopped"
	ContainerStateDeleting ContainerState = "deleting"
)

func (s ContainerState) CanTransitionTo(other ContainerState, isInternal bool) bool {
	switch s {
	case ContainerStateCreating:
		// only internal!
		return (isInternal && other == ContainerStateStarting) || other == ContainerStateDeleting
	case ContainerStateStarting:
		return other == ContainerStateRunning
	case ContainerStateRunning:
		return other == ContainerStateStopping
	case ContainerStateStopping:
		return other == ContainerStateStopped
	case ContainerStateStopped:
		// loop
		return other == ContainerStateStarting || other == ContainerStateDeleting
	case ContainerStateDeleting:
		return false
	default:
		return false
	}
}
