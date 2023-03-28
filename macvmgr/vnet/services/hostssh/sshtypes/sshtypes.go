package sshtypes

type SshMeta struct {
	Pwd        string
	RawCommand bool
	PtyStdin   bool
	PtyStdout  bool
	PtyStderr  bool
}
