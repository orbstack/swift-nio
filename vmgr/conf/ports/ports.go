package ports

const (
	// host
	HostDebugSSH      = 2222 // debug
	HostKubernetes    = 26443
	HostSconSSHPublic = 32222
	// host NFS is dynamically assigned

	// guest
	GuestDebugSSH        = 22 // debug
	GuestScon            = 8000
	GuestSconRPCInternal = 8001
	GuestSconSSH         = 2222
	GuestSconSSHPublic   = 2223
	GuestVcontrol        = 103
	GuestNFS             = 2049 // vsock
	GuestKrpc            = 9000
	// outside of ephemeral range
	GuestDocker = 2375
	GuestK8s    = 6443

	// host services for guest
	ServiceDNS  = 53
	ServiceNTP  = 123
	ServiceSFTP = 22323

	// secure services for guest (danger)
	SecureSvcHostSSH         = 22
	SecureSvcHostSSHAgent    = 23
	SecureSvcHcontrol        = 8300
	SecureSvcDockerRemoteCtx = 2376

	DockerMachineK8s       = 26443
	DockerMachineHttpProxy = 30817
)
