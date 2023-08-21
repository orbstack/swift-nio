package agent

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/agent/udpfwd"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

type PstubServer struct {
	unixListener net.Listener
}

// Docker userland-proxy server to reduce memory usage, speed up startup, and track listeners easily
func NewPstubServer() (*PstubServer, error) {
	l, err := util.ListenUnixWithPerms("/run/pstub.sock", 0600, 0, 0)
	if err != nil {
		return nil, err
	}

	return &PstubServer{
		unixListener: l,
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

	proxy, err := s.startServer(args)
	if err != nil {
		// send error
		_, _ = conn.Write([]byte(fmt.Sprintf("1\n%+v", err)))
		return err
	}
	defer proxy.Close()

	// send success
	_, _ = conn.Write([]byte("0\n"))

	// wait for conn to close (i.e. process exit)
	_, _ = io.Copy(io.Discard, conn)

	return nil
}

func (s *PstubServer) startServer(args []string) (io.Closer, error) {
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
		return nil, err
	}
	if proto == "" || hostIPRaw == "" || hostPort == 0 || containerIP == "" || containerPort == 0 {
		return nil, fmt.Errorf("missing required argument")
	}

	// we only support TCP and UDP, no SCTP
	if proto != "tcp" && proto != "udp" {
		return nil, fmt.Errorf("unsupported protocol %s", proto)
	}

	hostIP := net.ParseIP(hostIPRaw)
	if hostIP == nil {
		return nil, fmt.Errorf("invalid host IP")
	}
	dialIP := net.ParseIP(containerIP)
	if dialIP == nil {
		return nil, fmt.Errorf("invalid container IP")
	}

	// don't do tcp46
	ipVer := "6"
	if hostIP.To4() != nil {
		ipVer = "4"
	}

	switch proto {
	case "tcp":
		l, err := net.ListenTCP("tcp"+ipVer, &net.TCPAddr{
			IP:   hostIP,
			Port: hostPort,
		})
		// l, err := net.Listen("tcp", net.JoinHostPort(hostIP.String(), strconv.Itoa(hostPort)))
		if err != nil {
			return nil, err
		}

		proxy := tcpfwd.NewTCPProxy(l, false, uint16(containerPort), nil, dialIP)
		go proxy.Run()
		return proxy, nil

	case "udp":
		l, err := net.ListenUDP("udp"+ipVer, &net.UDPAddr{
			IP:   hostIP,
			Port: hostPort,
		})
		// l, err := net.ListenPacket("udp", net.JoinHostPort(hostIP.String(), strconv.Itoa(hostPort)))
		if err != nil {
			return nil, err
		}

		dialer := func(clientAddr *net.UDPAddr) (net.Conn, error) {
			return net.DialUDP("udp", nil, &net.UDPAddr{
				IP:   dialIP,
				Port: containerPort,
			})
		}
		proxy, err := udpfwd.NewUDPProxy(l, dialer)
		if err != nil {
			return nil, err
		}

		go proxy.Run()
		return proxy, nil
	default:
		panic("unreachable")
	}
}
