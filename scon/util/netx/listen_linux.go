//go:build linux

package netx

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func ListenTransparent(ctx context.Context, network, address string) (net.Listener, error) {
	lcfg := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var err2 error
			err := c.Control(func(fd uintptr) {
				// Go sets SO_REUSEADDR by default
				// we need IP_TRANSPARENT to be able to receive packets destined to a non-local ip, even though we're assigning this socket with bpf
				err2 = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TRANSPARENT, 1)
			})
			if err != nil {
				return err
			}

			return err2
		},
	}

	return lcfg.Listen(ctx, network, address)
}
