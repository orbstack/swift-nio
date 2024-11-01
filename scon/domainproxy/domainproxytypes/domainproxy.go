package domainproxytypes

import (
	"cmp"
	"net"
	"slices"
)

// represents an upstream of a domainproxy ip
// these are identified by the set of their domain names. this means order is irrelevant
// if two upstream objects have the same exact names, they're considered to point to the same thing
type Upstream struct {
	Names  []string
	IP     net.IP
	Docker bool
}

func slicesEqualUnordered[T cmp.Ordered](s1 []T, s2 []T) bool {
	if len(s1) != len(s2) {
		return false
	}

	return slices.Equal(
		slices.Sorted(slices.Values(s1)),
		slices.Sorted(slices.Values(s2)),
	)
}

func (u Upstream) IsValid() bool {
	return u.IP != nil
}

func (u Upstream) EqualNames(names []string) bool {
	return slicesEqualUnordered(u.Names, names)
}

// returns true if two upstream objects refer to the same upstream (compares name arrays as sets)
func (u Upstream) NamesEqual(other Upstream) bool {
	return u.EqualNames(other.Names)
}

// returns true if two upstream objects have the same routing information
func (u Upstream) ValEqual(other Upstream) bool {
	return u.IP.Equal(other.IP) && u.Docker == other.Docker
}
