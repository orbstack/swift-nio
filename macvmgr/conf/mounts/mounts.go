package mounts

import "github.com/kdrag0n/macvirt/macvmgr/conf/appid"

var (
	// linked paths don't need translation
	// excluded: /cores /opt/homebrew, /System due to Python platform.mac_ver()
	LinkedPaths = [...]string{"/Applications", "/Library", "/Users", "/Volumes", "/private"}
)

// linux
const (
	Virtiofs      = "/mnt/mac"
	LinuxExplicit = "/mnt/linux"

	Opt             = "/opt/" + appid.AppName + "-guest"
	Setctty         = Opt + "/setctty"
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

	Run            = Opt + "/run"
	SshAgentSocket = Run + "/host-ssh-agent.sock"
	HostSSHSocket  = Run + "/hostssh.sock"
	HcontrolSocket = Run + "/hcontrol.sock"
	ExtraCerts     = Run + "/extra-certs.crt"

	TmpSshAgentProxySocket   = "/dev/.orbstack/ssh-agent-proxy.sock"
	LaunchdSshAgentListeners = "/tmp/launchd-ssh-agent-listeners"
)

// mac
const (
	NfsDirName = "OrbStack"
)
