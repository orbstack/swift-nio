package ports

const (
	// host
	HostDebugSSH      = 2222 // debug
	HostSconSSHPublic = 62222
	HostVmControl     = 62420 // for Swift
	HostSconRPC       = 62421 // for Swift
	HostNFS           = 62429

	// guest
	GuestDebugSSH        = 22 // debug
	GuestScon            = 8000
	GuestSconRPCInternal = 8001
	GuestSconSSH         = 2222
	GuestSconSSHPublic   = 2223
	GuestVcontrol        = 103
	GuestNFS             = 2049
	// outside of ephemeral range
	GuestDocker = 2375

	// host services for guest
	ServiceDNS  = 53
	ServiceNTP  = 123
	ServiceSFTP = 22323

	// secure services for guest (danger)
	SecureSvcHostSSH      = 22
	SecureSvcHostSSHAgent = 23
	SecureSvcHcontrol     = 8300
)
