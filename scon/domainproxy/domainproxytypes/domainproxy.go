package domainproxytypes

import "net"

type DomainproxyUpstream struct {
	Ip     net.IP
	Id     string
	Docker bool
}
