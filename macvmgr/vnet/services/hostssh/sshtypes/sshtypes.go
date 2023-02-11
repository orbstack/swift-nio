package sshtypes

// tags for garble
type SshMeta struct {
	Pwd        string `json:"pwd"`
	RawCommand bool   `json:"raw_command"`
	PtyStdin   bool   `json:"pty_stdin"`
	PtyStdout  bool   `json:"pty_stdout"`
	PtyStderr  bool   `json:"pty_stderr"`
}
