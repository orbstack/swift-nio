package udpfwd

// Modified version of https://github.com/moby/moby/blob/master/cmd/docker-proxy/udp_proxy.go and
// https://github.com/moby/vpnkit/blob/master/go/pkg/libproxy/udp_proxy.go

import (
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kdrag0n/macvirt/macvmm/network/gonet"
	log "github.com/sirupsen/logrus"
)

const (
	// UDPConnTrackTimeout is the timeout used for UDP connection tracking
	UDPConnTrackTimeout = 90 * time.Second
	// UDPBufSize is the buffer size for the UDP proxy
	UDPBufSize = 65507
)

// A net.Addr where the IP is split into two fields so you can use it as a key
// in a map:
type connTrackKey struct {
	IPHigh uint64
	IPLow  uint64
	Port   int
}

func newConnTrackKey(addr *net.UDPAddr) *connTrackKey {
	if len(addr.IP) == net.IPv4len {
		return &connTrackKey{
			IPHigh: 0,
			IPLow:  uint64(binary.BigEndian.Uint32(addr.IP)),
			Port:   addr.Port,
		}
	}
	return &connTrackKey{
		IPHigh: binary.BigEndian.Uint64(addr.IP[:8]),
		IPLow:  binary.BigEndian.Uint64(addr.IP[8:]),
		Port:   addr.Port,
	}
}

type connTrackMap map[connTrackKey]net.Conn

// UDPProxy is proxy for which handles UDP datagrams. It implements the Proxy
// interface to handle UDP traffic forwarding between the frontend and backend
// addresses.
type UDPProxy struct {
	listener       *autoStoppingListener
	dialer         func() (net.Conn, error)
	connTrackTable connTrackMap
	connTrackLock  sync.Mutex
}

// External connection source addr -> local (virtual) source addr
var localExtConnTrack = make(map[connTrackKey]*net.UDPAddr)
var localExtConnTrackLock sync.RWMutex

func LookupExternalConn(localAddr *net.UDPAddr) *net.UDPAddr {
	localExtConnTrackLock.RLock()
	defer localExtConnTrackLock.RUnlock()
	return localExtConnTrack[*newConnTrackKey(localAddr)]
}

// NewUDPProxy creates a new UDPProxy.
func NewUDPProxy(listener *autoStoppingListener, dialer func() (net.Conn, error)) (*UDPProxy, error) {
	return &UDPProxy{
		listener:       listener,
		connTrackTable: make(connTrackMap),
		dialer:         dialer,
	}, nil
}

func (proxy *UDPProxy) replyLoop(proxyConn net.Conn, clientAddr net.Addr, clientKey *connTrackKey, localExtKey *connTrackKey) {
	defer func() {
		proxy.connTrackLock.Lock()
		delete(proxy.connTrackTable, *clientKey)
		proxy.connTrackLock.Unlock()

		go func() {
			// Keep conntrack entry for a while for ICMP time exceeded
			time.Sleep(UDPConnTrackTimeout)
			// CAS in case a new connection
			localExtConnTrackLock.Lock()
			if newAddr, ok := localExtConnTrack[*localExtKey]; ok && newAddr == clientAddr.(*net.UDPAddr) {
				delete(localExtConnTrack, *localExtKey)
			}
			localExtConnTrackLock.Unlock()
		}()

		proxyConn.Close()
	}()

	readBuf := make([]byte, UDPBufSize)
	for {
		_ = proxyConn.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
	again:
		read, err := proxyConn.Read(readBuf)
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
	readBuf := make([]byte, UDPBufSize)
	lastTtl := uint8(64)
	for {
		read, from, err := proxy.listener.ReadFrom(readBuf)
		if err != nil {
			// NOTE: Apparently ReadFrom doesn't return
			// ECONNREFUSED like Read do (see comment in
			// UDPProxy.replyLoop)
			if !isClosedError(err) {
				log.Debugf("Stopping udp proxy (%s)", err)
			}
			break
		}

		fromKey := newConnTrackKey(from.(*net.UDPAddr))
		proxy.connTrackLock.Lock()
		proxyConn, hit := proxy.connTrackTable[*fromKey]
		if !hit {
			proxyConn, err = proxy.dialer()
			if err != nil {
				log.Errorf("Can't proxy a datagram to udp: %s\n", err)
				proxy.connTrackLock.Unlock()
				continue
			}
			proxy.connTrackTable[*fromKey] = proxyConn

			// Track local source address
			localExtKey := newConnTrackKey(proxyConn.LocalAddr().(*net.UDPAddr))
			localExtConnTrackLock.Lock()
			localExtConnTrack[*localExtKey] = from.(*net.UDPAddr)
			localExtConnTrackLock.Unlock()

			go proxy.replyLoop(proxyConn, from, fromKey, localExtKey)
		}
		proxy.connTrackLock.Unlock()

		// Set TTL
		newTtl := proxy.listener.underlying.LastTTL
		if useTtl && newTtl != lastTtl {
			lastTtl = newTtl
			rawConn, err := proxyConn.(*net.UDPConn).SyscallConn()
			if err != nil {
				log.Errorf("Can't set TTL on UDP socket: %s\n", err)
			} else {
				rawConn.Control(func(fd uintptr) {
					var err error
					if proxyConn.LocalAddr().(*net.UDPAddr).IP.To4() != nil {
						err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, int(newTtl))
					} else {
						err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS, int(newTtl))
					}
					if err != nil {
						log.Errorf("Can't set TTL on UDP socket: %s\n", err)
					}
				})
			}
		}

		_ = proxyConn.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
		written, err := proxyConn.Write(readBuf[:read])
		if err != nil {
			log.Errorf("Can't proxy a datagram to udp: %s\n", err)
			break
		}
		if written != read {
			log.Errorf("Can't proxy a datagram to udp: short write\n")
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

func isClosedError(err error) bool {
	/* This comparison is ugly, but unfortunately, net.go doesn't export errClosing.
	 * See:
	 * http://golang.org/src/pkg/net/net.go
	 * https://code.google.com/p/go/issues/detail?id=4337
	 * https://groups.google.com/forum/#!msg/golang-nuts/0_aaCvBmOcM/SptmDyX1XJMJ
	 */
	return strings.HasSuffix(err.Error(), "use of closed network connection")
}

type autoStoppingListener struct {
	underlying *gonet.UDPConn
}

func (l *autoStoppingListener) ReadFrom(b []byte) (int, net.Addr, error) {
	_ = l.underlying.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
	return l.underlying.ReadFrom(b)
}

func (l *autoStoppingListener) WriteTo(b []byte, addr net.Addr) (int, error) {
	_ = l.underlying.SetReadDeadline(time.Now().Add(UDPConnTrackTimeout))
	return l.underlying.WriteTo(b, addr)
}

func (l *autoStoppingListener) SetReadDeadline(t time.Time) error {
	return l.underlying.SetReadDeadline(t)
}

func (l *autoStoppingListener) Close() error {
	return l.underlying.Close()
}
