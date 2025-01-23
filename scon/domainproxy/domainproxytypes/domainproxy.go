package domainproxytypes

import (
	"net"
	"slices"
)

// represents an upstream of a domainproxy ip
// these are identified by the set of their domain names. this means order is irrelevant
// if two upstream objects have the same exact names, they're considered to point to the same thing
type Upstream struct {
	Names       []string
	namesSorted []string // used for comparison and map looksups

	IP net.IP

	Host Host
}

func NewUpstream(Names []string, IP net.IP, Host Host) Upstream {
	return Upstream{
		Names:       Names,
		namesSorted: slices.Sorted(slices.Values(Names)),

		IP:   IP,
		Host: Host,
	}
}

func (u Upstream) IsValid() bool {
	return u.IP != nil
}

func (u Upstream) NamesSorted() []string {
	if u.namesSorted == nil {
		return slices.Sorted(slices.Values(u.Names))
	}

	return u.namesSorted
}

func (u Upstream) EqualNames(names []string) bool {
	if len(u.Names) != len(names) {
		return false
	}

	return slices.Equal(u.NamesSorted(), slices.Sorted(slices.Values(names)))
}

// returns true if two upstream objects refer to the same upstream (compares name arrays as sets)
func (u Upstream) NamesEqual(other Upstream) bool {
	if len(u.Names) != len(other.Names) {
		return false
	}

	return slices.Equal(u.NamesSorted(), other.NamesSorted())
}

// returns true if two upstream objects have the same routing information
func (u Upstream) ValEqual(other Upstream) bool {
	return u.IP.Equal(other.IP) && u.Host.Equal(other.Host)
}

// represents a container or machine
type Host struct {
	ID     string
	Docker bool
	K8s    bool
}

func (h Host) Equal(other Host) bool {
	return h.ID == other.ID && h.Docker == other.Docker && h.K8s == other.K8s
}
