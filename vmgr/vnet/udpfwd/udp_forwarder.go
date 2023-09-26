package udpfwd

// Modified version of https://github.com/moby/moby/blob/master/cmd/docker-proxy/udp_proxy.go and
// https://github.com/moby/vpnkit/blob/master/go/pkg/libproxy/udp_proxy.go

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	// UDPConnTrackTimeout is the timeout used for UDP connection tracking
	UDPConnTrackTimeout = 30 * time.Second

	// one packet. we can't do recvmsg_x/sendmsg_x
	defaultBufferSize = 65536
)

// A net.Addr where the IP is split into two fields so you can use it as a key
// in a map:
type connTrackKey struct {
	IP   netip.Addr
	Port int
}

func newConnTrackKey(addr *net.UDPAddr) *connTrackKey {
	ip, ok := netip.AddrFromSlice(addr.IP)
	if !ok {
		return nil
	}

	return &connTrackKey{
		IP:   ip,
		Port: addr.Port,
	}
}

type extConnEntry struct {
	conn    net.Conn
	lastTTL uint8
}

type connTrackMap map[connTrackKey]*extConnEntry

// UDPProxy is proxy for which handles UDP datagrams. It implements the Proxy
// interface to handle UDP traffic forwarding between the frontend and backend
// addresses.
type UDPProxy struct {
	listener       net.PacketConn
	dialer         func(*net.UDPAddr) (net.Conn, error)
	connTrackTable connTrackMap
	connTrackLock  sync.Mutex
	trackExtConn   bool
}

// External connection source addr -> local (virtual) source addr
var localExtConns = make(map[connTrackKey]*net.UDPAddr)
var localExtConnsLock sync.Mutex

func LookupExternalConn(localAddr *net.UDPAddr) *net.UDPAddr {
	localExtConnsLock.Lock()
	defer localExtConnsLock.Unlock()
	return localExtConns[*newConnTrackKey(localAddr)]
}

// NewUDPProxy creates a new UDPProxy.
func NewUDPProxy(listener net.PacketConn, dialer func(*net.UDPAddr) (net.Conn, error), trackExtConn bool) (*UDPProxy, error) {
	return &UDPProxy{
		listener:       listener,
		connTrackTable: make(connTrackMap),
		dialer:         dialer,
		trackExtConn:   trackExtConn,
	}, nil
}

func (proxy *UDPProxy) replyLoop(extConn net.Conn, clientAddr net.Addr, clientKey *connTrackKey, localExtKey *connTrackKey) {
	defer func() {
		proxy.connTrackLock.Lock()
		delete(proxy.connTrackTable, *clientKey)
		proxy.connTrackLock.Unlock()

		if proxy.trackExtConn {
			// remove conntrack entry for ICMP time exceeded - already passed conntrack timeout
			// CAS in case a new connection
			localExtConnsLock.Lock()
			if newAddr, ok := localExtConns[*localExtKey]; ok && newAddr == clientAddr.(*net.UDPAddr) {
				delete(localExtConns, *localExtKey)
			}
			localExtConnsLock.Unlock()
		}

		extConn.Close()
	}()

	readBuf := make([]byte, defaultBufferSize)
	for {
		_ = extConn.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
	again:
		read, err := extConn.Read(readBuf)
		if err != nil {
			if err, ok := err.(*net.OpError); ok && err.Err == syscall.ECONNREFUSED {
				// This will happen if the last write failed
				// (e.g: nothing is actually listening on the
				// proxied port on the container), ignore it
				// and continue until UDPConnTrackTimeout
				// expires:
				goto again
			}
			return
		}
		written, err := proxy.listener.WriteTo(readBuf[:read], clientAddr)
		if err != nil {
			return
		}
		if written != read {
			return
		}
	}
}

// Run starts forwarding the traffic using UDP.
func (proxy *UDPProxy) Run(useTtl bool) {
	defer proxy.Close()

	readBuf := make([]byte, defaultBufferSize)
	for {
		read, from, err := proxy.listener.ReadFrom(readBuf)
		if err != nil {
			// NOTE: Apparently ReadFrom doesn't return
			// ECONNREFUSED like Read do (see comment in
			// UDPProxy.replyLoop)
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) && !errors.Is(err, gonet.ErrTimeout) {
				logrus.Error("UDP proxy conn ReadFrom() failed: ", err)
			}
			break
		}

		fromKey := newConnTrackKey(from.(*net.UDPAddr))
		proxy.connTrackLock.Lock()
		ext, hit := proxy.connTrackTable[*fromKey]
		if !hit {
			newConn, err := proxy.dialer(from.(*net.UDPAddr))
			if err != nil {
				logrus.Error("UDP dial failed: ", err)
				proxy.connTrackLock.Unlock()
				continue
			}
			ext = &extConnEntry{
				conn:    newConn,
				lastTTL: uint8(64),
			}
			proxy.connTrackTable[*fromKey] = ext

			// Track local source address
			localExtKey := newConnTrackKey(ext.conn.LocalAddr().(*net.UDPAddr))
			if proxy.trackExtConn {
				localExtConnsLock.Lock()
				localExtConns[*localExtKey] = from.(*net.UDPAddr)
				localExtConnsLock.Unlock()
			}

			go proxy.replyLoop(ext.conn, from, fromKey, localExtKey)
		}
		proxy.connTrackLock.Unlock()

		// Set TTL
		if useTtl {
			if connWrapper, ok := proxy.listener.(*autoStoppingListener); ok {
				newTtl := connWrapper.UDPConn.LastTTL
				if newTtl != ext.lastTTL {
					rawConn, err := ext.conn.(*net.UDPConn).SyscallConn()
					if err != nil {
						logrus.Error("UDP set TTL failed ", err)
					} else {
						err = rawConn.Control(func(fd uintptr) {
							var err error
							if ext.conn.LocalAddr().(*net.UDPAddr).IP.To4() != nil {
								err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TTL, int(newTtl))
							} else {
								err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, int(newTtl))
							}
							if err != nil {
								logrus.Error("UDP set TTL failed ", err)
							}
						})
						if err != nil {
							logrus.Error("UDP set TTL failed ", err)
						}
					}
					// if setting it this time failed, it probably won't work next time
					ext.lastTTL = newTtl
				}
			}
		}

		_ = ext.conn.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
		written, err := ext.conn.Write(readBuf[:read])
		if err != nil {
			if !errors.Is(err, unix.ENOBUFS) {
				logrus.Error("UDP write failed", err)
				break
			}
		} else if written != read {
			logrus.Error("UDP write failed: short write")
			break
		}
	}
}

// Close stops forwarding the traffic.
func (proxy *UDPProxy) Close() error {
	proxy.listener.Close()
	proxy.connTrackLock.Lock()
	defer proxy.connTrackLock.Unlock()
	for _, entry := range proxy.connTrackTable {
		entry.conn.Close()
	}
	return nil
}

type autoStoppingListener struct {
	*gonet.UDPConn
}

func (l *autoStoppingListener) ReadFrom(b []byte) (int, net.Addr, error) {
	_ = l.UDPConn.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
	return l.UDPConn.ReadFrom(b)
}

func (l *autoStoppingListener) WriteTo(b []byte, addr net.Addr) (int, error) {
	_ = l.UDPConn.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
	return l.UDPConn.WriteTo(b, addr)
}
