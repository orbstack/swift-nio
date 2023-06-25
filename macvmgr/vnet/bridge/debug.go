// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package bridge

import (
	"fmt"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func logMessages(msgs []route.Message) {
	for i, msg := range msgs {
		switch msg := msg.(type) {
		default:
			fmt.Printf("  [%d] %T\n", i, msg)
		case *route.InterfaceAddrMessage:
			fmt.Printf("  [%d] InterfaceAddrMessage: ver=%d, type=%v, flags=0x%x, idx=%v\n",
				i, msg.Version, typeStr(msg.Type), msg.Flags, msg.Index)
			logAddrs(msg.Addrs)
		case *route.InterfaceMulticastAddrMessage:
			fmt.Printf("  [%d] InterfaceMulticastAddrMessage: ver=%d, type=%v, flags=0x%x, idx=%v\n",
				i, msg.Version, typeStr(msg.Type), msg.Flags, msg.Index)
			logAddrs(msg.Addrs)
		case *route.RouteMessage:
			fmt.Printf("  [%d] RouteMessage: ver=%d, type=%v, flags=0x%x, idx=%v, id=%v, seq=%v, err=%v\n",
				i, msg.Version, typeStr(msg.Type), msg.Flags, msg.Index, msg.ID, msg.Seq, msg.Err)
			logAddrs(msg.Addrs)
		}
	}
}

func logAddrs(addrs []route.Addr) {
	for i, a := range addrs {
		if a == nil {
			continue
		}
		fmt.Printf("      %v = %v\n", rtaxName(i), fmtAddr(a))
	}
}
func fmtAddr(a route.Addr) any {
	if a == nil {
		return nil
	}
	if ip := ipOfAddr(a); ip.IsValid() {
		return ip
	}
	switch a := a.(type) {
	case *route.LinkAddr:
		return fmt.Sprintf("[LinkAddr idx=%v name=%q addr=%x]", a.Index, a.Name, a.Addr)
	default:
		return fmt.Sprintf("%T: %+v", a, a)
	}
}

// See https://github.com/apple/darwin-xnu/blob/main/bsd/net/route.h
func rtaxName(i int) string {
	switch i {
	case unix.RTAX_DST:
		return "dst"
	case unix.RTAX_GATEWAY:
		return "gateway"
	case unix.RTAX_NETMASK:
		return "netmask"
	case unix.RTAX_GENMASK:
		return "genmask"
	case unix.RTAX_IFP: // "interface name sockaddr present"
		return "IFP"
	case unix.RTAX_IFA: // "interface addr sockaddr present"
		return "IFA"
	case unix.RTAX_AUTHOR:
		return "author"
	case unix.RTAX_BRD:
		return "BRD"
	}
	return fmt.Sprint(i)
}

func typeStr(typ int) string {
	switch typ {
	case unix.RTM_ADD:
		return "RTM_ADD"
	case unix.RTM_CHANGE:
		return "RTM_CHANGE"
	case unix.RTM_DELADDR:
		return "RTM_DELADDR"
	case unix.RTM_DELETE:
		return "RTM_DELETE"
	case unix.RTM_DELMADDR:
		return "RTM_DELMADDR"
	case unix.RTM_GET:
		return "RTM_GET"
	case unix.RTM_GET2:
		return "RTM_GET2"
	case unix.RTM_IFINFO:
		return "RTM_IFINFO"
	case unix.RTM_IFINFO2:
		return "RTM_IFINFO2"
	case unix.RTM_LOCK:
		return "RTM_LOCK"
	case unix.RTM_LOSING:
		return "RTM_LOSING"
	case unix.RTM_MISS:
		return "RTM_MISS"
	case unix.RTM_NEWADDR:
		return "RTM_NEWADDR"
	case unix.RTM_NEWMADDR:
		return "RTM_NEWMADDR"
	case unix.RTM_NEWMADDR2:
		return "RTM_NEWMADDR2"
	case unix.RTM_OLDADD:
		return "RTM_OLDADD"
	case unix.RTM_OLDDEL:
		return "RTM_OLDDEL"
	case unix.RTM_REDIRECT:
		return "RTM_REDIRECT"
	case unix.RTM_RESOLVE:
		return "RTM_RESOLVE"
	default:
		return fmt.Sprintf("unknown type %d", typ)
	}
}
