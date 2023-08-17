package ports

const (
	// host
	HostDebugSSH      = 2222 // debug
	HostKubernetes    = 6443
	HostSconSSHPublic = 32222
	HostVmControl     = 42506 // for Swift
	HostSconRPC       = 42507 // for Swift
	// host NFS is dynamically assigned

	// guest
	GuestDebugSSH        = 22 // debug
	GuestScon            = 8000
	GuestSconRPCInternal = 8001
	GuestSconSSH         = 2222
	GuestSconSSHPublic   = 2223
	GuestVcontrol        = 103
	GuestNFS             = 2049
	GuestKrpc            = 9000
	// outside of ephemeral range
	GuestDocker = 2375

	// host services for guest
	ServiceDNS  = 53
	ServiceNTP  = 123
	ServiceSFTP = 22323

	// secure services for guest (danger)
	SecureSvcHostSSH         = 22
	SecureSvcHostSSHAgent    = 23
	SecureSvcHcontrol        = 8300
	SecureSvcDockerRemoteCtx = 2376
)
