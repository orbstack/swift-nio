package mounts

import "github.com/orbstack/macvirt/vmgr/conf/appid"

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

	Opt             = "/opt/" + appid.AppName + "-guest"
	Pstub           = Opt + "/pstub"
	Cattach         = Opt + "/cattach"
	Bin             = Opt + "/bin"
	Macctl          = Bin + "/macctl"
	UserCmdLinks    = Opt + "/data/bin/cmdlinks"
	DefaultCmdLinks = Bin

	Etc          = Opt + "/etc"
	ProfileEarly = Etc + "/profile-early"
	ProfileLate  = Etc + "/profile-late"
	SshConfig    = Etc + "/ssh_config"
	ResolvConf   = Etc + "/resolv.conf"

	BinHiprio             = Opt + "/bin-hiprio"
	DefaultHiprioCmdLinks = BinHiprio

	Run                 = Opt + "/run"
	SshAgentSocket      = Run + "/host-ssh-agent.sock"
	SshAgentProxySocket = Run + "/ssh-agent-proxy.sock"
	HostSSHSocket       = Run + "/hostssh.sock"
	HcontrolSocket      = Run + "/hcontrol.sock"
	SconGuestSocket     = Run + "/scon-guest.sock"
	ExtraCerts          = Run + "/extra-certs.crt"

	DockerSocket = "/var/run/docker.sock"

	// host paths
	LaunchdSshAgentListeners  = "/opt/orb/launchd-ssh-agent-listeners"
	DockerSshAgentProxySocket = "/run/docker-ssh-agent-proxy.sock"
	NfsContainers             = "/nfs/containers"
)

// mac
const (
	NfsDirName = "OrbStack"
)
