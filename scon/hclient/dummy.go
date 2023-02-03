package hclient

import (
	"errors"
	"net"
	"net/rpc"
	"os/user"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/sirupsen/logrus"
)

type HcontrolServer struct {
	activeHostsForwards map[string]struct{}
}

func (h *HcontrolServer) Ping(_ None, _ *None) error {
	return nil
}

func (h *HcontrolServer) StartForward(spec ForwardSpec, _ *None) error {
	logrus.Infof("hcontrol: start forward: g %s -> h %s", spec.Guest, spec.Host)
	if _, ok := h.activeHostsForwards[spec.Host]; ok {
		return errors.New("forward already exists")
	}
	h.activeHostsForwards[spec.Host] = struct{}{}
	return nil
}

func (h *HcontrolServer) StopForward(spec ForwardSpec, _ *None) error {
	logrus.Infof("hcontrol: stop forward: g %s -> h %s", spec.Guest, spec.Host)
	if _, ok := h.activeHostsForwards[spec.Host]; !ok {
		return errors.New("forward doesn't exist")
	}
	delete(h.activeHostsForwards, spec.Host)
	return nil
}

func (h *HcontrolServer) GetUser(_ None, reply *user.User) error {
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

func (h *HcontrolServer) GetTimezone(_ *None, reply *string) error {
	*reply = "America/Los_Angeles"
	return nil
}

func (h *HcontrolServer) GetSSHPublicKey(_ None, reply *string) error {
	*reply = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ/wCg/nWi0s+OYvjdW6JdxYaXpoO/fZvzwu0RRszPir"
	return nil
}

func (h *HcontrolServer) GetSSHAgentSocket(_ None, reply *string) error {
	*reply = ""
	return nil
}

func StartDummyServer() error {
	server := &HcontrolServer{
		activeHostsForwards: make(map[string]struct{}),
	}
	rpcServer := rpc.NewServer()
	rpcServer.RegisterName("hc", server)

	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(ports.SecureSvcHcontrol))
	if err != nil {
		return err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return nil
}
