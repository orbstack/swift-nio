package bridge

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/orbstack/macvirt/macvmgr/vnet/netconf"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

var (
	netipSconHostBridgeIP4 = netip.MustParseAddr(netconf.SconHostBridgeIP4)
)

// interfaceIndexFor returns the interface index that we should bind to in
// order to send traffic to the provided address.
// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause
func interfaceIndexFor(addr netip.Addr, canRecurse bool) (int, error) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)
	if err != nil {
		return 0, fmt.Errorf("creating AF_ROUTE socket: %w", err)
	}
	defer unix.Close(fd)

	var routeAddr route.Addr
	if addr.Is4() {
		routeAddr = &route.Inet4Addr{IP: addr.As4()}
	} else {
		routeAddr = &route.Inet6Addr{IP: addr.As16()}
	}

	rm := route.RouteMessage{
		// NOTE: This is unix.RTM_VERSION, but we want to pin this to a
		// particular constant so that it doesn't change under us if
		// the x/sys/unix package changes down the road. Currently this
		// is 0x5 on both Darwin x86 and ARM64.
		Version: 0x5,
		Type:    unix.RTM_GET,
		Flags:   unix.RTF_UP,
		ID:      uintptr(os.Getpid()),
		Seq:     1,
		Addrs: []route.Addr{
			unix.RTAX_DST: routeAddr,
		},
	}
	b, err := rm.Marshal()
	if err != nil {
		return 0, fmt.Errorf("marshaling RouteMessage: %w", err)
	}
	_, err = unix.Write(fd, b)
	if err != nil {
		return 0, fmt.Errorf("writing message: %w", err)
	}

	// On macOS, the RTM_GET call should return exactly one route message.
	// Given the following sizes and constants:
	//    - sizeof(struct rt_msghdr) = 92
	//    - RTAX_MAX = 8
	//    - sizeof(struct sockaddr_in6) = 28
	//    - sizeof(struct sockaddr_in) = 16
	//    - sizeof(struct sockaddr_dl) = 20
	//
	// The maximum buffer size should be:
	//    sizeof(struct rt_msghdr) + RTAX_MAX*sizeof(struct sockaddr_in6)
	//    = 92 + 8*28
	//    = 316
	//
	// During my testing, responses are typically ~120 bytes.
	//
	// We provide a much larger buffer just in case we're off by a bit, or
	// the kernel decides to return more than one message; 2048 bytes
	// should be plenty here. This also means we can do a single Read.
	var buf [2048]byte
	n, err := unix.Read(fd, buf[:])
	if err != nil {
		return 0, fmt.Errorf("reading message: %w", err)
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, buf[:n])
	if err != nil {
		return 0, fmt.Errorf("route.ParseRIB: %w", err)
	}
	if len(msgs) == 0 {
		return 0, fmt.Errorf("no messages")
	}

	for _, msg := range msgs {
		rm, ok := msg.(*route.RouteMessage)
		if !ok {
			continue
		}
		if rm.Version < 3 || rm.Version > 5 || rm.Type != unix.RTM_GET {
			continue
		}
		if len(rm.Addrs) < unix.RTAX_GATEWAY {
			continue
		}

		switch addr := rm.Addrs[unix.RTAX_GATEWAY].(type) {
		case *route.LinkAddr:
			return addr.Index, nil
		case *route.Inet4Addr:
			// We can get a gateway IP; recursively call ourselves
			// (exactly once) to get the link (and thus index) for
			// the gateway IP.
			if canRecurse {
				return interfaceIndexFor(netip.AddrFrom4(addr.IP), false)
			}
		case *route.Inet6Addr:
			// As above.
			if canRecurse {
				return interfaceIndexFor(netip.AddrFrom16(addr.IP), false)
			}
		default:
			// Unknown type; skip it
			continue
		}
	}

	return 0, fmt.Errorf("no valid address found")
}

func IsMachineRouteCorrect() (bool, error) {
	// look up interface for route
	ifaceIndex, err := interfaceIndexFor(netipSconHostBridgeIP4, true)
	if err != nil {
		return false, err
	}

	// cheap way to check:
	// look up interface name and make sure it's "bridge"...
	// mainly trying to protect against utun network extensions
	iface, err := net.InterfaceByIndex(ifaceIndex)
	if err != nil {
		return false, err
	}

	return strings.HasPrefix(iface.Name, "bridge"), nil
}
