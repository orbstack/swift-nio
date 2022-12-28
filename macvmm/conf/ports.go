package conf

const (
	// host
	HostPortSSH      = 2222
	HostPortNFS      = 62429
	HostPortNFSVsock = HostPortNFS + 1

	// guest
	GuestPortSSH    = 22
	GuestPortNFS    = 2049
	GuestPortDocker = 62375
)
