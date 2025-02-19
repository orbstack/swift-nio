package domainproxytypes

import (
	"net"
	"slices"
)

// represents an upstream of a domainproxy ip
// this is identified by its Host
type Upstream struct {
	Host Host

	Names []string
	IP    net.IP

	HTTPPortOverride  uint16
	HTTPSPortOverride uint16
}

func (u *Upstream) IsValid() bool {
	return u.IP != nil
}

func (u *Upstream) Equal(other Upstream) bool {
	return u.Host.Equal(other.Host)
}

// returns true if two upstream objects have the same routing information
func (u *Upstream) ValEqual(other Upstream) bool {
	return slices.Equal(u.Names, other.Names) && u.IP.Equal(other.IP)
}

type HostType int

const (
	HostTypeMachine HostType = iota
	HostTypeDocker
	HostTypeK8s
)

// represents a machine or container
type Host struct {
	Type HostType
	ID   string
}

func (h Host) Equal(other Host) bool {
	return h.ID == other.ID && h.Type == other.Type
}
