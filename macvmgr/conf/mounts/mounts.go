package mounts

var (
	// linked paths don't need translation
	// excluded: /cores
	LinkedPaths = [...]string{"/Applications", "/Library", "/System", "/Users", "/Volumes", "/opt/homebrew", "/private"}
)

const (
	VirtiofsMountpoint = "/mnt/mac"

	Opt             = "/opt/macvirt-guest"
	Bin             = Opt + "/bin"
	Macctl          = Bin + "/macctl"
	UserCmdLinks    = Opt + "/data/bin/cmdlinks"
	DefaultCmdLinks = Bin

	BinHiprio             = Opt + "/bin-hiprio"
	DefualtHiprioCmdLinks = BinHiprio

	Run            = Opt + "/run"
	SshAgentSocket = Run + "/ssh-agent.sock"
	HostSSHSocket  = Run + "/hostssh.sock"
	HcontrolSocket = Run + "/hcontrol.sock"
)
