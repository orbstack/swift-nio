package hcsrv

import (
	"crypto/rand"
	"encoding/base32"
	"net/rpc"

	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	HcontrolPort = 8300
)

var (
	instanceToken = genToken()
)

type HcontrolServer struct {
	n *vnet.Network
}

func (h *HcontrolServer) Ping(args *None, reply *None) error {
	return nil
}

// func (h *HcontrolServer) StartForward() error {

// }

// func (h *HcontrolServer) StopForward() error {

// }

type None struct{}

type HostForwarder interface {
}

func genToken() string {
	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}

	// to base32
	b32str := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return b32str
}

func GetCurrentToken() string {
	return instanceToken
}

func ListenHcontrol(stack *stack.Stack, address tcpip.Address) (*HcontrolServer, error) {
	server := &HcontrolServer{}
	rpcServer := rpc.NewServer()
	rpcServer.RegisterName("hc", server)

	listener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: HcontrolPort,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return nil, err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return server, nil
}
