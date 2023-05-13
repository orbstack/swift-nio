package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	sknlgrpInetTCPDestroy  = 1
	sknlgrpInetUDPDestroy  = 2
	sknlgrpInet6TCPDestroy = 3
	sknlgrpInet6UDPDestroy = 4

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

func monitorInetDiag(c *Container, nlFile *os.File) error {
	defer nlFile.Close()

	// subscribe to group
	rawConn, err := nlFile.SyscallConn()
	if err != nil {
		return err
	}
	var err2 error
	err = rawConn.Control(func(fd uintptr) {
		var groups uint32
		groups |= 1 << (sknlgrpInetTCPDestroy - 1)
		groups |= 1 << (sknlgrpInetUDPDestroy - 1)
		groups |= 1 << (sknlgrpInet6TCPDestroy - 1)
		groups |= 1 << (sknlgrpInet6UDPDestroy - 1)
		sa := unix.SockaddrNetlink{
			Family: unix.AF_NETLINK,
			Groups: groups,
		}
		err2 = unix.Bind(int(fd), &sa)
	})
	if err != nil {
		return err
	}
	if err2 != nil {
		return err2
	}

	// receive messages
	buf := make([]byte, 32768)
	for {
		n, err := nlFile.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return nil
			} else {
				return err
			}
		}
		if n < unix.NLMSG_HDRLEN {
			continue
		}

		// handle async to avoid blocking netlink
		go func() {
			sock := &netlink.Socket{}
			err = deserializeDiagSocket(sock, buf[:n][unix.NLMSG_HDRLEN:])
			if err != nil {
				logrus.Errorf("failed to deserialize socket: %v", err)
				return
			}

			// src port != 0
			// remote ip and port = 0
			if sock.ID.SourcePort != 0 && sock.ID.DestinationPort == 0 && sock.ID.Destination.IsUnspecified() {
				// strip 4-in-6 mapped ipv4 addresses
				ip4 := sock.ID.Source.To4()
				if ip4 != nil {
					sock.ID.Source = ip4
				}

				localNetip, ok := netip.AddrFromSlice(sock.ID.Source)
				if !ok {
					logrus.Errorf("failed to convert net.IP to netip.Addr: %v", sock.ID.Source)
					return
				}

				// tcp forward exists?
				// TODO read nlattr/rtattr INET_DIAG_PROTOCOL
				// https://github.com/shemminger/iproute2/blob/d7f81def84013202f27cf84ee455f644ff685443/misc/ss.c#L3391
				agentSpec := sysnet.ProcListener{
					Addr:  localNetip,
					Port:  uint16(sock.ID.SourcePort),
					Proto: sysnet.ProtoTCP,
				}
				if c.manager.checkForward(c, agentSpec) {
					c.triggerListenersUpdate()
				}

				// udp forward exists?
				agentSpec.Proto = sysnet.ProtoUDP
				if c.manager.checkForward(c, agentSpec) {
					c.triggerListenersUpdate()
				}
			}
		}()
	}
}
