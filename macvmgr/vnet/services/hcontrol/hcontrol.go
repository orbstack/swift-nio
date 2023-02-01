package hcsrv

import (
	"crypto/rand"
	"encoding/base32"
	"net/rpc"
	"os"
	"os/user"
	"reflect"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

var (
	instanceToken = genToken()
)

// Never obfuscate the HcontrolServer type (garble)
var _ = reflect.TypeOf(HcontrolServer{})

type HcontrolServer struct {
	n *vnet.Network
}

func (h *HcontrolServer) Ping(_ *None, _ *None) error {
	return nil
}

func (h *HcontrolServer) StartForward(spec vnet.ForwardSpec, _ *None) error {
	return h.n.StartForward(spec)
}

func (h *HcontrolServer) StopForward(spec vnet.ForwardSpec, _ *None) error {
	return h.n.StopForward(spec)
}

func (h *HcontrolServer) GetUser(_ *None, reply *user.User) error {
	u, err := user.Current()
	if err != nil {
		return err
	}

	*reply = *u
	return nil
}

func (h *HcontrolServer) GetTimezone(_ *None, reply *string) error {
	linkDest, err := os.Readlink("/etc/localtime")
	if err != nil {
		return err
	}

	// take the part after /var/db/timezone/zoneinfo/
	*reply = strings.TrimPrefix(linkDest, "/var/db/timezone/zoneinfo/")
	return nil
}

func (h *HcontrolServer) GetSSHPublicKey(_ None, reply *string) error {
	data, err := os.ReadFile(conf.ExtraSshDir() + "/id_ed25519.pub")
	if err != nil {
		return err
	}

	*reply = strings.TrimSpace(string(data))
	return nil
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

func ListenHcontrol(n *vnet.Network, address tcpip.Address) (*HcontrolServer, error) {
	server := &HcontrolServer{
		n: n,
	}
	rpcServer := rpc.NewServer()
	rpcServer.RegisterName("hc", server)

	listener, err := gonet.ListenTCP(n.Stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.SecureSvcHcontrol,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return nil, err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return server, nil
}
