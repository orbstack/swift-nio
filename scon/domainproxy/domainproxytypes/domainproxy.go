package domainproxytypes

import "net"

type DomainproxyUpstream struct {
	IP     net.IP
	Names  []string
	Docker bool
}
