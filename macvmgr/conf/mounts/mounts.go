package mounts

var (
	// linked paths don't need translation
	// excluded: /cores
	LinkedPaths = [...]string{"/Applications", "/Library", "/System", "/Users", "/Volumes", "/opt/homebrew", "/private"}
)

const (
	VirtiofsMountpoint = "/mnt/mac"
)
