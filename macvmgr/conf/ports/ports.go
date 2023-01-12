package ports

const (
	// host
	HostSSH      = 2222
	HostHcontrol = 3333
	HostNFS      = 62429

	// guest
	GuestSSH      = 22
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
