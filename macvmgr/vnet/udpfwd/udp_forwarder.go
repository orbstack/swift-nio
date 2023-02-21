package udpfwd

// Modified version of https://github.com/moby/moby/blob/master/cmd/docker-proxy/udp_proxy.go and
// https://github.com/moby/vpnkit/blob/master/go/pkg/libproxy/udp_proxy.go

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	// UDPConnTrackTimeout is the timeout used for UDP connection tracking
	UDPConnTrackTimeout = 60 * time.Second
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

type connTrackMap map[connTrackKey]net.Conn

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
			go func() {
				// Keep conntrack entry for a while for ICMP time exceeded
				time.Sleep(UDPConnTrackTimeout)
				// CAS in case a new connection
				localExtConnsLock.Lock()
				if newAddr, ok := localExtConns[*localExtKey]; ok && newAddr == clientAddr.(*net.UDPAddr) {
					delete(localExtConns, *localExtKey)
				}
				localExtConnsLock.Unlock()
			}()
		}

		extConn.Close()
	}()

	readBuf := make([]byte, 65536)
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
	readBuf := make([]byte, 65536)
	lastTtl := uint8(64)
	for {
		read, from, err := proxy.listener.ReadFrom(readBuf)
		if err != nil {
			// NOTE: Apparently ReadFrom doesn't return
			// ECONNREFUSED like Read do (see comment in
			// UDPProxy.replyLoop)
			if !errors.Is(err, net.ErrClosed) {
				logrus.Error("UDP proxy conn ReadFrom() failed: ", err)
			}
			break
		}

		fromKey := newConnTrackKey(from.(*net.UDPAddr))
		proxy.connTrackLock.Lock()
		extConn, hit := proxy.connTrackTable[*fromKey]
		if !hit {
			extConn, err = proxy.dialer(from.(*net.UDPAddr))
			if err != nil {
				logrus.Error("UDP dial failed: ", err)
				proxy.connTrackLock.Unlock()
				continue
			}
			proxy.connTrackTable[*fromKey] = extConn

			// Track local source address
			localExtKey := newConnTrackKey(extConn.LocalAddr().(*net.UDPAddr))
			if proxy.trackExtConn {
				localExtConnsLock.Lock()
				localExtConns[*localExtKey] = from.(*net.UDPAddr)
				localExtConnsLock.Unlock()
			}

			go proxy.replyLoop(extConn, from, fromKey, localExtKey)
		}
		proxy.connTrackLock.Unlock()

		// Set TTL
		if useTtl {
			connWrapper, ok := proxy.listener.(*autoStoppingListener)
			if ok {
				newTtl := connWrapper.UDPConn.LastTTL
				if newTtl != lastTtl {
					rawConn, err := extConn.(*net.UDPConn).SyscallConn()
					if err != nil {
						logrus.Error("UDP set TTL failed ", err)
					} else {
						rawConn.Control(func(fd uintptr) {
							var err error
							if extConn.LocalAddr().(*net.UDPAddr).IP.To4() != nil {
								err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, int(newTtl))
							} else {
								err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS, int(newTtl))
							}
							if err != nil {
								logrus.Error("UDP set TTL failed ", err)
							}
						})
					}
					// if setting it this time failed, it probably won't work next time
					lastTtl = newTtl
				}
			}
		}

		_ = extConn.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
		written, err := extConn.Write(readBuf[:read])
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
	for _, conn := range proxy.connTrackTable {
		conn.Close()
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
