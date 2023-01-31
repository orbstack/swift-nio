package ports

const (
	// host
	HostSSH           = 2222 // debug
	HostVmControl     = 62420
	HostSconSSHPublic = 62421
	HostNFS           = 62429

	// guest
	GuestDebugSSH      = 22 // debug
	GuestScon          = 8000
	GuestSconSSH       = 2222
	GuestSconSSHPublic = 2223
	GuestVcontrol      = 103
	GuestNFS           = 2049
	// outside of ephemeral range
	GuestDocker = 2375

	// host services for guest
	ServiceDNS  = 53
	ServiceNTP  = 123
	ServiceSFTP = 22323

	// secure services for guest
	SecureSvcHostSSH  = 22   // danger
	SecureSvcHcontrol = 8300 // danger
)
