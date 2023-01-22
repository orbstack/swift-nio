package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"syscall"
	"unsafe"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	sknlgrpInetTCPDestroy  = 1
	sknlgrpInetUDPDestroy  = 2
	sknlgrpInet6TCPDestroy = 3
	sknlgrpInet6UDPDestroy = 4

	cSsClose = 7

	sizeofSocketID      = 0x30
	sizeofSocketRequest = sizeofSocketID + 0x8
	sizeofSocket        = 80
)

type readBuffer struct {
	Bytes []byte
	pos   int
}

func (b *readBuffer) Read() byte {
	c := b.Bytes[b.pos]
	b.pos++
	return c
}

func (b *readBuffer) Next(n int) []byte {
	s := b.Bytes[b.pos : b.pos+n]
	b.pos += n
	return s
}

func deserializeDiagSocket(s *netlink.Socket, b []byte) error {
	native := binary.LittleEndian
	networkOrder := binary.BigEndian
	if len(b) < sizeofSocket {
		return fmt.Errorf("socket data short read (%d); want %d", len(b), sizeofSocket)
	}
	rb := readBuffer{Bytes: b}
	fmt.Println(hex.Dump(b))
	s.Family = rb.Read()
	s.State = rb.Read()
	s.Timer = rb.Read()
	s.Retrans = rb.Read()
	s.ID.SourcePort = networkOrder.Uint16(rb.Next(2))
	s.ID.DestinationPort = networkOrder.Uint16(rb.Next(2))
	if s.Family == unix.AF_INET {
		s.ID.Source = net.IPv4(rb.Read(), rb.Read(), rb.Read(), rb.Read())
		rb.Next(12)
		s.ID.Destination = net.IPv4(rb.Read(), rb.Read(), rb.Read(), rb.Read())
		rb.Next(12)
	} else {
		s.ID.Source = net.IP(rb.Next(16))
		s.ID.Destination = net.IP(rb.Next(16))
	}
	s.ID.Interface = native.Uint32(rb.Next(4))
	s.ID.Cookie[0] = native.Uint32(rb.Next(4))
	s.ID.Cookie[1] = native.Uint32(rb.Next(4))
	s.Expires = native.Uint32(rb.Next(4))
	s.RQueue = native.Uint32(rb.Next(4))
	s.WQueue = native.Uint32(rb.Next(4))
	s.UID = native.Uint32(rb.Next(4))
	s.INode = native.Uint32(rb.Next(4))
	return nil
}

const (
	sizeofRtAttr = 0x4
)

type netlinkAttr struct {
	Attr  unix.NlAttr
	Value []byte
}

func rtaAlignOf(attrlen int) int {
	return (attrlen + unix.RTA_ALIGNTO - 1) & ^(unix.RTA_ALIGNTO - 1)
}

func netlinkRouteAttrAndValue(b []byte) (*unix.NlAttr, []byte, int, error) {
	a := (*unix.NlAttr)(unsafe.Pointer(&b[0]))
	if int(a.Len) < sizeofRtAttr || int(a.Len) > len(b) {
		return nil, nil, 0, unix.EINVAL
	}
	return a, b[sizeofRtAttr:], rtaAlignOf(int(a.Len)), nil
}

func parseNetlinkRouteAttr(m *syscall.NetlinkMessage) ([]netlinkAttr, error) {
	b := m.Data[sizeofSocket:]
	var attrs []netlinkAttr
	for len(b) >= sizeofRtAttr {
		a, vbuf, alen, err := netlinkRouteAttrAndValue(b)
		if err != nil {
			return nil, err
		}
		ra := netlinkAttr{Attr: *a, Value: vbuf[:int(a.Len)-sizeofRtAttr]}
		attrs = append(attrs, ra)
		b = b[alen:]
	}
	return attrs, nil
}

func monitorInetDiag(c *Container, nlFile *os.File) error {
	defer nlFile.Close()

	// subscribe to group
	fd := nlFile.Fd()
	var groups uint32
	groups |= 1 << (sknlgrpInetTCPDestroy - 1)
	groups |= 1 << (sknlgrpInetUDPDestroy - 1)
	groups |= 1 << (sknlgrpInet6TCPDestroy - 1)
	groups |= 1 << (sknlgrpInet6UDPDestroy - 1)
	sa := unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: groups,
	}
	if err := unix.Bind(int(fd), &sa); err != nil {
		return err
	}

	// receive messages
	buf := make([]byte, 32768)
	for {
		// TODO will this hang forever?
		n, _, err := unix.Recvfrom(int(fd), buf, 0)
		if err != nil {
			return err
		}
		if n < unix.NLMSG_HDRLEN {
			continue
		}

		// handle async to avoid blocking netlink
		go func() {
			msgs, err := syscall.ParseNetlinkMessage(buf[:n])
			if err != nil {
				logrus.Errorf("failed to parse netlink message: %v", err)
				return
			}
			msg := msgs[0]

			sock := &netlink.Socket{}
			err = deserializeDiagSocket(sock, msg.Data)
			if err != nil {
				logrus.Errorf("failed to deserialize socket: %v", err)
				return
			}

			// attrs
			attrs, err := parseNetlinkRouteAttr(&msg)
			if err != nil {
				logrus.Errorf("failed to parse netlink route attr: %v", err)
				return
			}

			logrus.Debug("socket closed: ", sock)
			sjson, _ := json.Marshal(sock)
			logrus.Debug("socket json: ", string(sjson))
			logrus.Debug("attrs: ", attrs)

			// src port != 0
			// remote ip and port = 0
			/*
				if sock.State == cSsClose && (sock.Family == unix.AF_INET || sock.Family == unix.AF_INET6) && sock.ID.SourcePort != 0 {
					// forward exists?
					localNetip, ok := netip.AddrFromSlice(sock.ID.Source)
					if !ok {
						logrus.Errorf("failed to convert net.IP to netip.Addr: %v", sock.ID.Source)
						continue
					}

					proto := agent.ProtoTCP
					agentSpec := agent.ProcListener{
						Addr: localNetip,
						Port: uint16(sock.ID.SourcePort),
					}
					if c.manager.checkForward(c, agentSpec) {
						c.triggerListenersUpdate()
					}
				}*/
		}()
	}
}
