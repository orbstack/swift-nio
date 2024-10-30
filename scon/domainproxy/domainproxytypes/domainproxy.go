package domainproxytypes

import "net"

type DomainproxyUpstream struct {
	IP     net.IP
	Id     string
	Docker bool
}
