package hcsrv

import (
	"crypto/rand"
	"encoding/base32"
	"net/rpc"
	"reflect"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

var (
	instanceToken = genToken()
)

// Never obfuscate the HcontrolServer type (garble)
var _ = reflect.TypeOf(HcontrolServer{})

type HcontrolServer struct {
	n *vnet.Network
}

func (h *HcontrolServer) Ping(args *None, reply *None) error {
	return nil
}

func (h *HcontrolServer) StartForward(spec vnet.ForwardSpec, reply *None) error {
	return h.n.StartForward(spec)
}

func (h *HcontrolServer) StopForward(spec vnet.ForwardSpec, reply *None) error {
	return h.n.StopForward(spec)
}

type None struct{}

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
		Port: ports.ServiceHcontrol,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return nil, err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return server, nil
}
