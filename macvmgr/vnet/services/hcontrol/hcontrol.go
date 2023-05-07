package hcsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/dockertypes"
	"github.com/kdrag0n/macvirt/macvmgr/drm"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/fsnotify"
	"github.com/kdrag0n/macvirt/macvmgr/guihelper"
	"github.com/kdrag0n/macvirt/macvmgr/guihelper/guitypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hcontrol/htypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/sshagent"
	"github.com/kdrag0n/macvirt/macvmgr/vzf"
	"github.com/muja/goconfig"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

type HcontrolServer struct {
	n         *vnet.Network
	drmClient *drm.DrmClient

	fsnotifyMu   sync.Mutex
	fsnotifyRefs map[string]int
	FsNotifier   *fsnotify.VmNotifier
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

func (h *HcontrolServer) ReadDockerDaemonConfig(_ None, reply *string) error {
	data, err := os.ReadFile(conf.DockerDaemonConfig())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// write an empty config for user convenience if it doesn't exist
			err = os.WriteFile(conf.DockerDaemonConfig(), []byte("{}"), 0644)
			if err != nil {
				return err
			}

			*reply = ""
			return nil
		}

		return err
	}

	*reply = string(data)
	return nil
}

func (h *HcontrolServer) GetExtraCaCertificates(_ None, reply *[]string) error {
	certs, err := vzf.SwextSecurityGetExtraCaCerts()
	if err != nil {
		return err
	}

	*reply = certs
	return nil
}

func (h *HcontrolServer) Notify(n guitypes.Notification, _ *None) error {
	return guihelper.Notify(n)
}

func (h *HcontrolServer) AddFsnotifyRef(path string, _ *None) error {
	h.fsnotifyMu.Lock()
	defer h.fsnotifyMu.Unlock()

	h.fsnotifyRefs[path]++

	if h.fsnotifyRefs[path] == 1 {
		err := h.FsNotifier.Add(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *HcontrolServer) RemoveFsnotifyRef(path string, _ *None) error {
	h.fsnotifyMu.Lock()
	defer h.fsnotifyMu.Unlock()

	if h.fsnotifyRefs[path] == 0 {
		return fmt.Errorf("path not tracked in hcontrol: %s", path)
	}

	h.fsnotifyRefs[path]--

	if h.fsnotifyRefs[path] == 0 {
		err := h.FsNotifier.Remove(path)
		if err != nil {
			return err
		}
		delete(h.fsnotifyRefs, path)
	}

	if h.fsnotifyRefs[path] < 0 {
		return fmt.Errorf("negative refcount for %s", path)
	}

	return nil
}

func (h *HcontrolServer) ClearFsnotifyRefs(_ None, _ *None) error {
	h.fsnotifyMu.Lock()
	defer h.fsnotifyMu.Unlock()

	for path, count := range h.fsnotifyRefs {
		if count > 0 {
			err := h.FsNotifier.Remove(path)
			if err != nil {
				return err
			}
		}
	}

	h.fsnotifyRefs = make(map[string]int)
	return nil
}

func (h *HcontrolServer) OnDockerUIEvent(event dockertypes.UIEvent, _ *None) error {
	// encode to json
	data, err := json.Marshal(&event)
	if err != nil {
		return err
	}

	// notify GUI
	logrus.WithField("event", event).Debug("sending docker UI event to GUI")
	vzf.SwextIpcNotifyDockerEvent(string(data))
	return nil
}

type None struct{}

func ListenHcontrol(n *vnet.Network, address tcpip.Address) (*HcontrolServer, error) {
	server := &HcontrolServer{
		n:            n,
		drmClient:    drm.Client(),
		fsnotifyRefs: make(map[string]int),
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
