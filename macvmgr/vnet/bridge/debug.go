package bridge

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func (m *RouteMon) logMessages(msgs []route.Message) {
	for i, msg := range msgs {
		switch msg := msg.(type) {
		default:
			logrus.Debugf("  [%d] %T", i, msg)
		case *route.InterfaceAddrMessage:
			logrus.Debugf("  [%d] InterfaceAddrMessage: ver=%d, type=%v, flags=0x%x, idx=%v",
				i, msg.Version, msg.Type, msg.Flags, msg.Index)
			m.logAddrs(msg.Addrs)
		case *route.InterfaceMulticastAddrMessage:
			logrus.Debugf("  [%d] InterfaceMulticastAddrMessage: ver=%d, type=%v, flags=0x%x, idx=%v",
				i, msg.Version, msg.Type, msg.Flags, msg.Index)
			m.logAddrs(msg.Addrs)
		case *route.RouteMessage:
			logrus.Debugf("  [%d] RouteMessage: ver=%d, type=%v, flags=0x%x, idx=%v, id=%v, seq=%v, err=%v",
				i, msg.Version, msg.Type, msg.Flags, msg.Index, msg.ID, msg.Seq, msg.Err)
			m.logAddrs(msg.Addrs)
		}
	}
}

func (m *RouteMon) logAddrs(addrs []route.Addr) {
	for i, a := range addrs {
		if a == nil {
			continue
		}
		logrus.Debugf("      %v = %v", rtaxName(i), fmtAddr(a))
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
