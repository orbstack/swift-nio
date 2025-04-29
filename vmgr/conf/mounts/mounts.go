package mounts

var (
	// linked paths don't need translation
	// excluded: /cores /opt/homebrew, /System due to Python platform.mac_ver()
	// Docker has more because lower chance of conflict
	LinkedPaths = [...]string{"/Applications", "/Library", "/Users", "/Volumes", "/private"}
)

// linux
const (
	Virtiofs      = "/mnt/mac"
	LinuxExplicit = "/mnt/linux"
	Machines      = "/mnt/machines"

	Opt             = "/opt/orbstack-guest"
	Pstub           = Opt + "/pstub"
	WormholeAttach  = Opt + "/wormhole-attach"
	WormholeStub    = Opt + "/wormhole-stub"
	Bin             = Opt + "/bin"
	Macctl          = Bin + "/macctl"
	UserCmdLinks    = Opt + "/data/bin/cmdlinks"
	DefaultCmdLinks = Bin
	LinuxTools      = Opt + "/lib/linux-tools/current"
	Starry          = Opt + "/starry"

	Etc          = Opt + "/etc"
	ProfileEarly = Etc + "/profile-early"
	ProfileLate  = Etc + "/profile-late"
	SshConfig    = Etc + "/ssh_config"
	ResolvConf   = Etc + "/resolv.conf"

	BinHiprio             = Opt + "/bin-hiprio"
	DefaultHiprioCmdLinks = BinHiprio

	Data = Opt + "/data"

	Run                 = Opt + "/run"
	SshAgentSocket      = Run + "/host-ssh-agent.sock"
	SshAgentProxySocket = Run + "/ssh-agent-proxy.sock"
	HostSSHSocket       = Run + "/hostssh.sock"
	HcontrolSocket      = Run + "/hcontrol.sock"
	SconGuestSocket     = Run + "/scon-guest.sock"
	ExtraCerts          = Run + "/extra-certs.crt"

	HostRun                 = "/run/orbstack-guest-run"
	HostSshAgentSocket      = HostRun + "/host-ssh-agent.sock"
	HostSshAgentProxySocket = HostRun + "/ssh-agent-proxy.sock"
	HostHostSSHSocket       = HostRun + "/hostssh.sock"
	HostHcontrolSocket      = HostRun + "/hcontrol.sock"
	HostSconGuestSocket     = HostRun + "/scon-guest.sock"
	HostExtraCerts          = HostRun + "/extra-certs.crt"
	HostDockerSocket        = HostRun + "/docker.sock"

	// docker guest paths
	DockerSocket         = "/var/run/docker.sock"
	DockerRuncWrapSocket = "/run/rc.sock" // same on host

	// VM host paths
	LaunchdSshAgentListeners  = "/opt/orb/launchd-ssh-agent-listeners"
	DockerSshAgentProxySocket = "/run/docker-ssh-agent-proxy.sock"
	NfsContainers             = "/nfs/containers"
	WormholeRootfs            = "/opt/wormhole-rootfs"
	WormholeOverlay           = "/mnt/wormhole-overlay"
	WormholeOverlayNix        = WormholeOverlay + "/nix"
	WormholeUnifiedNix        = "/mnt/wormhole-unified/nix"
)

// mac
const (
	NfsDirName = "OrbStack"
)
