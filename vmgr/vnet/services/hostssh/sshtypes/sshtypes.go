package sshtypes

const (
	KeyMeta = "__ORB_CMETA"

	WormholeIDDocker = "_docker"
	WormholeIDHost   = "_ovm"
)

type SshMeta struct {
	Pwd              string
	Argv0            *string
	RawCommand       bool
	PtyStdin         bool
	PtyStdout        bool
	PtyStderr        bool
	WormholeFallback bool
}
