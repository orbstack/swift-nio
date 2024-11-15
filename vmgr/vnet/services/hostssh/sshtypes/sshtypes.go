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
	InitPid  int    `json:"a"`
	DrmToken string `json:"b"`

	ContainerWorkdir string   `json:"c,omitempty"`
	ContainerEnv     []string `json:"d"`

	EntryShellCmd string `json:"e,omitempty"`
}

type WormholeRuntimeState struct {
	RootfsFd            int `json:"a,omitempty"`
	WormholeMountTreeFd int `json:"b"`
	ExitCodePipeWriteFd int `json:"c"`
	LogFd               int `json:"d"`
}
