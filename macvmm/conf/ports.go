package conf

const (
	// host
	HostPortSSH      = 2222
	HostPortHcontrol = 3333
	HostPortNFS      = 62429
	HostPortNFSVsock = HostPortNFS + 1

	// guest
	GuestPortSSH      = 22
	GuestPortVcontrol = 103
	GuestPortNFS      = 2049
	GuestPortDocker   = 62375
)
