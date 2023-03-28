package hcsrv

import (
	"errors"
	"io"
	"net/rpc"
	"os"
	"os/user"
	"reflect"
	"strconv"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/drm"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/guihelper"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hcontrol/htypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/sshagent"
	"github.com/muja/goconfig"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

// Never obfuscate the HcontrolServer type (garble)
var _ = reflect.TypeOf(HcontrolServer{})

type HcontrolServer struct {
	n         *vnet.Network
	drmClient *drm.DrmClient
}

func (h *HcontrolServer) Ping(_ *None, _ *None) error {
	return nil
}

func (h *HcontrolServer) StartForward(spec vnet.ForwardSpec, _ *None) error {
	_, err := h.n.StartForward(spec)
	if err != nil {
		return err
	}
	return nil
}

func (h *HcontrolServer) StopForward(spec vnet.ForwardSpec, _ *None) error {
	return h.n.StopForward(spec)
}

func (h *HcontrolServer) GetUser(_ *None, reply *htypes.User) error {
	u, err := user.Current()
	if err != nil {
		return err
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}

	*reply = htypes.User{
		Uid:      uid,
		Gid:      gid,
		Username: u.Username,
		Name:     u.Name,
		HomeDir:  u.HomeDir,
	}

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

func (h *HcontrolServer) GetSSHAgentSockets(_ None, reply *htypes.SSHAgentSockets) error {
	*reply = sshagent.GetAgentSockets()
	return nil
}

func (h *HcontrolServer) GetGitConfig(_ None, reply *map[string]string) error {
	data, err := os.ReadFile(conf.HomeDir() + "/.gitconfig")
	if err != nil {
		return err
	}

	config, _, err := goconfig.Parse(data)
	if err != nil {
		return err
	}

	*reply = config
	return nil
}

func (h *HcontrolServer) GetLastDrmResult(_ None, reply *drmtypes.Result) error {
	result, err := h.drmClient.UpdateResult()
	if err != nil {
		return err
	}
	if result == nil {
		return errors.New("no result available")
	}

	*reply = *result
	return nil
}

func (h *HcontrolServer) Notify(n guihelper.Notification, _ *None) error {
	return guihelper.Notify(n)
}

type None struct{}

func ListenHcontrol(n *vnet.Network, address tcpip.Address) (*HcontrolServer, error) {
	server := &HcontrolServer{
		n:         n,
		drmClient: drm.Client(),
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
		for {
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					logrus.WithError(err).Error("hcontrol: accept failed")
				}
				return
			}
			go rpcServer.ServeConn(conn)
		}
	}()

	return server, nil
}
