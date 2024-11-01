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

type WormholeConfig struct {
	// renamed for obfuscation, as this may be user-visible
	InitPid             int    `json:"a"`
	RootfsFd            int    `json:"i,omitempty"`
	WormholeMountTreeFd int    `json:"b"`
	ExitCodePipeWriteFd int    `json:"c"`
	LogFd               int    `json:"d"`
	DrmToken            string `json:"e"`

	ContainerWorkdir string   `json:"f,omitempty"`
	ContainerEnv     []string `json:"g"`

	EntryShellCmd string `json:"h,omitempty"`
}
