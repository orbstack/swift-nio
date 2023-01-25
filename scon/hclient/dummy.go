package hclient

import (
	"net"
	"net/rpc"
	"os/user"
	"reflect"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/sirupsen/logrus"
)

// Never obfuscate the HcontrolServer type (garble)
var _ = reflect.TypeOf(HcontrolServer{})

type HcontrolServer struct {
}

func (h *HcontrolServer) Ping(_ *None, _ *None) error {
	return nil
}

func (h *HcontrolServer) StartForward(spec ForwardSpec, _ *None) error {
	logrus.Infof("hcontrol: start forward: g %s -> h %s", spec.Guest, spec.Host)
	return nil
}

func (h *HcontrolServer) StopForward(spec ForwardSpec, _ *None) error {
	logrus.Infof("hcontrol: stop forward: g %s -> h %s", spec.Guest, spec.Host)
	return nil
}

func (h *HcontrolServer) GetUser(_ *None, reply *user.User) error {
	_, err := user.Current()
	if err != nil {
		return err
	}

	*reply = user.User{
		Uid:      "1000",
		Gid:      "1000",
		Username: "dragon",
		Name:     "Dragon",
		HomeDir:  "/home/dragon",
	}
	return nil
}

func StartDummyServer() error {
	server := &HcontrolServer{}
	rpcServer := rpc.NewServer()
	rpcServer.RegisterName("hc", server)

	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(ports.HostHcontrol))
	if err != nil {
		return err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return nil
}
