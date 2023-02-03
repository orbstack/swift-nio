package mounts

import "github.com/kdrag0n/macvirt/macvmgr/conf/appid"

var (
	// linked paths don't need translation
	// excluded: /cores /opt/homebrew
	LinkedPaths = [...]string{"/Applications", "/Library", "/System", "/Users", "/Volumes", "/private"}
)

const (
	VirtiofsMountpoint = "/mnt/mac"

	Opt             = "/opt/" + appid.Codename + "-guest"
	Bin             = Opt + "/bin"
	Macctl          = Bin + "/macctl"
	UserCmdLinks    = Opt + "/data/bin/cmdlinks"
	DefaultCmdLinks = Bin

	Etc          = Opt + "/etc"
	ProfileEarly = Etc + "/profile-early"
	ProfileLate  = Etc + "/profile-late"

	BinHiprio             = Opt + "/bin-hiprio"
	DefualtHiprioCmdLinks = BinHiprio

	Run            = Opt + "/run"
	SshAgentSocket = Run + "/host-ssh-agent.sock"
	HostSSHSocket  = Run + "/hostssh.sock"
	HcontrolSocket = Run + "/hcontrol.sock"
)
