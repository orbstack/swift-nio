package mounts

var (
	// linked paths don't need translation
	LinkedPaths = [...]string{"/Applications", "/Library", "/System", "/Users", "/Volumes", "/cores", "/opt/homebrew", "/private"}
)

const (
	VirtiofsMountpoint = "/mnt/mac"
)
