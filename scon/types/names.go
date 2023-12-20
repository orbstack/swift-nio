package types

import "regexp"

const (
	ContainerDocker   = "docker"
	ContainerIDDocker = "01GQQVF6C60000000000DOCKER"

	// currently same
	ContainerK8s   = "k8s"
	ContainerIDK8s = "01GQQVF6C60000000000DOCKER"
)

var (
	// min 2 chars, disallows hidden files (^.)
	// hostname rules: can't contain _, can't start with -, and '.' has special meaning (nixos doesn't like it)
	ContainerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-.]+$`)
	// .orb.internal domains, plus "default" special ssh name
	ContainerNameBlacklist = []string{"default", "vm", "host", "services", "gateway", ContainerK8s, ContainerDocker}
)
