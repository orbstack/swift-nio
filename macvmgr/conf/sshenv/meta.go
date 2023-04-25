package sshenv

const (
	KeyMeta = "__ORB_CMETA"
)

type CmdMeta struct {
	Pwd        string
	Argv0      *string
	RawCommand bool
	PtyStdin   bool
	PtyStdout  bool
	PtyStderr  bool
}
