package sshtypes

type SshMeta struct {
	Pwd             string
	RawCommand      bool
	DisableStdinPty bool
}

type MacMeta struct {
	User string
}
