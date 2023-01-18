package sshtypes

type SshMeta struct {
	Pwd        string `json:"pwd"`
	RawCommand bool   `json:"raw_command"`
	PtyStdin   bool   `json:"pty_stdin"`
	PtyStdout  bool   `json:"pty_stdout"`
	PtyStderr  bool   `json:"pty_stderr"`
}

type MacMeta struct {
	User string `json:"user"`
}
