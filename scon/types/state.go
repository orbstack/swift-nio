package types

type ContainerState string

const (
	ContainerStateCreating ContainerState = "creating"
	ContainerStateStarting ContainerState = "starting"
	ContainerStateRunning  ContainerState = "running"
	ContainerStateStopping ContainerState = "stopping"
	ContainerStateStopped  ContainerState = "stopped"
	ContainerStateDeleting ContainerState = "deleting"

	// only present in persisted state
	ContainerStateProvisioning ContainerState = "provisioning"
)

func (s ContainerState) CanTransitionTo(other ContainerState, isInternal bool) bool {
	switch s {
	case ContainerStateCreating:
		// only internal transitions allowed for start/stop!
		return other == ContainerStateDeleting || (isInternal && (other == ContainerStateStarting || other == ContainerStateStopped))
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

func (s ContainerState) IsInitializing() bool {
	return s == ContainerStateCreating || s == ContainerStateProvisioning
}
