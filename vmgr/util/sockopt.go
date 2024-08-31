package util

import (
	"syscall"

	"github.com/orbstack/macvirt/scon/util"
	"golang.org/x/sys/unix"
)

func SetConnTTL(conn syscall.RawConn, isIpv6 bool, ttl int) error {
	return util.UseRawConn(conn, func(fd int) error {
		if isIpv6 {
			return unix.SetsockoptInt(fd, syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS, ttl)
		} else {
			return unix.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
		}
	})
}
