package sshtypes

type SshMeta struct {
	Pwd        string
	Argv0      *string
	RawCommand bool
	PtyStdin   bool
	PtyStdout  bool
	PtyStderr  bool
}
