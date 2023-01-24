package ports

const (
	// host
	HostSSH      = 2222 // debug
	HostHcontrol = 62420
	HostNFS      = 62429

	// guest
	GuestDebugSSH = 22 // debug
	GuestSconSSH  = 2222
	GuestVcontrol = 103
	GuestNFS      = 2049
	GuestDocker   = 62375

	// host services for guest
	ServiceHostSSH  = 22 // danger
	ServiceDNS      = 53
	ServiceNTP      = 123
	ServiceHcontrol = 8300 // danger
	ServiceSFTP     = 22323
)
