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
		// stopped: if failed to start (users cannot trigger this state)
		return other == ContainerStateRunning || other == ContainerStateStopped
	case ContainerStateRunning:
		// stopped: if machine powered off (users cannot trigger this state)
		return other == ContainerStateStopping || other == ContainerStateStopped
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
