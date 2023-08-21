package agent

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/agent/udpfwd"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/sirupsen/logrus"
)

type PstubServer struct {
	unixListener net.Listener

	mu         sync.Mutex
	serverKeys map[sysnet.ListenerKey]struct{}
}

// Docker userland-proxy server to reduce memory usage, speed up startup, and track listeners easily for iptables accel
func NewPstubServer() (*PstubServer, error) {
	l, err := util.ListenUnixWithPerms("/run/pstub.sock", 0600, 0, 0)
	if err != nil {
		return nil, err
	}

	return &PstubServer{
		unixListener: l,
		serverKeys:   make(map[sysnet.ListenerKey]struct{}),
	}, nil
}

func (s *PstubServer) Serve() error {
	for {
		conn, err := s.unixListener.Accept()
		if err != nil {
			return err
		}

		// synchronous b/c docker is the only client
		go func() {
			err := s.handleConn(conn.(*net.UnixConn))
			if err != nil {
				logrus.WithError(err).Error("pstub connection failed")
			}
		}()
	}
}

func (s *PstubServer) handleConn(conn *net.UnixConn) error {
	defer conn.Close()

	// read length
	var lenBuf [4]byte
	_, err := io.ReadFull(conn, lenBuf[:])
	if err != nil {
		return fmt.Errorf("read length: %w", err)
	}
	argsLen := binary.LittleEndian.Uint32(lenBuf[:])
	if argsLen > 1024 {
		return fmt.Errorf("args too long")
	}

	// read args
	var argBuf [1024]byte
	n, err := conn.Read(argBuf[:argsLen])
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	args := strings.Split(string(argBuf[:n]), "\x00")
	logrus.WithField("args", args).Debug("start pstub")

	proxy, key, err := s.startServer(args)
	if err != nil {
		// send error
		_, _ = conn.Write([]byte(fmt.Sprintf("1\n%+v", err)))
		return err
	}
	defer proxy.Close()

	s.addServerKey(key)
	defer s.removeServerKey(key)

	// send success
	_, _ = conn.Write([]byte("0\n"))

	// wait for conn to close (i.e. process exit)
	_, _ = io.Copy(io.Discard, conn)

	return nil
}

func (s *PstubServer) startServer(args []string) (io.Closer, sysnet.ListenerKey, error) {
	flags := flag.NewFlagSet("pstub", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	var proto string
	flags.StringVar(&proto, "proto", "", "")
	var hostIPRaw string
	flags.StringVar(&hostIPRaw, "host-ip", "", "")
	var hostPort int
	flags.IntVar(&hostPort, "host-port", 0, "")
	var containerIP string
	flags.StringVar(&containerIP, "container-ip", "", "")
	var containerPort int
	flags.IntVar(&containerPort, "container-port", 0, "")

	err := flags.Parse(args)
	if err != nil {
		return nil, sysnet.ListenerKey{}, err
	}
	if proto == "" || hostIPRaw == "" || hostPort == 0 || containerIP == "" || containerPort == 0 {
		return nil, sysnet.ListenerKey{}, fmt.Errorf("missing required argument")
	}

	hostIP := net.ParseIP(hostIPRaw)
	if hostIP == nil {
		return nil, sysnet.ListenerKey{}, fmt.Errorf("invalid host IP")
	}
	dialIP := net.ParseIP(containerIP)
	if dialIP == nil {
		return nil, sysnet.ListenerKey{}, fmt.Errorf("invalid container IP")
	}

	// don't do tcp46
	ipVer := "6"
	if hostIP.To4() != nil {
		ipVer = "4"
	}

	// we only support TCP and UDP, no SCTP
	switch proto {
	case "tcp":
		l, err := net.ListenTCP("tcp"+ipVer, &net.TCPAddr{
			IP:   hostIP,
			Port: hostPort,
		})
		if err != nil {
			return nil, sysnet.ListenerKey{}, err
		}

		proxy := tcpfwd.NewTCPProxy(l, false, uint16(containerPort), nil, dialIP)
		go proxy.Run()
		return proxy, makeServerKey(proto, hostIP, hostPort), nil

	case "udp":
		l, err := net.ListenUDP("udp"+ipVer, &net.UDPAddr{
			IP:   hostIP,
			Port: hostPort,
		})
		if err != nil {
			return nil, sysnet.ListenerKey{}, err
		}

		dialer := func(clientAddr *net.UDPAddr) (net.Conn, error) {
			return net.DialUDP("udp", nil, &net.UDPAddr{
				IP:   dialIP,
				Port: containerPort,
			})
		}
		proxy, err := udpfwd.NewUDPProxy(l, dialer)
		if err != nil {
			return nil, sysnet.ListenerKey{}, err
		}

		go proxy.Run()
		return proxy, makeServerKey(proto, hostIP, hostPort), nil

	default:
		return nil, sysnet.ListenerKey{}, fmt.Errorf("unsupported protocol %s", proto)
	}
}

func (s *PstubServer) Close() error {
	return s.unixListener.Close()
}

func makeServerKey(proto string, hostIP net.IP, hostPort int) sysnet.ListenerKey {
	addr, ok := netip.AddrFromSlice(hostIP)
	if !ok {
		panic("invalid host IP")
	}
	addr = addr.Unmap()

	return sysnet.ListenerKey{
		AddrPort: netip.AddrPortFrom(addr, uint16(hostPort)),
		Proto:    sysnet.TransportProtocol(proto),
	}
}

func (s *PstubServer) addServerKey(key sysnet.ListenerKey) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.serverKeys[key] = struct{}{}
}

func (s *PstubServer) removeServerKey(key sysnet.ListenerKey) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.serverKeys, key)
}

func (s *PstubServer) OwnsWildcardSpec(spec ProxySpec, proto sysnet.TransportProtocol) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	addr := netip.IPv4Unspecified()
	if spec.IsIPv6 {
		addr = netip.IPv6Unspecified()
	}
	wantKey := sysnet.ListenerKey{
		AddrPort: netip.AddrPortFrom(addr, spec.Port),
		Proto:    proto,
	}
	_, ok := s.serverKeys[wantKey]
	return ok
}
