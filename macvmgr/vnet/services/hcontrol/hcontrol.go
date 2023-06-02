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

	"github.com/muja/goconfig"
	"github.com/orbstack/macvirt/macvmgr/conf"
	"github.com/orbstack/macvirt/macvmgr/conf/coredir"
	"github.com/orbstack/macvirt/macvmgr/conf/nfsmnt"
	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/macvmgr/dockertypes"
	"github.com/orbstack/macvirt/macvmgr/drm"
	"github.com/orbstack/macvirt/macvmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/macvmgr/fsnotify"
	"github.com/orbstack/macvirt/macvmgr/guihelper"
	"github.com/orbstack/macvirt/macvmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/macvmgr/vnet"
	"github.com/orbstack/macvirt/macvmgr/vnet/gonet"
	"github.com/orbstack/macvirt/macvmgr/vnet/services/hcontrol/htypes"
	"github.com/orbstack/macvirt/macvmgr/vnet/services/sshagent"
	"github.com/orbstack/macvirt/macvmgr/vzf"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

const (
	nfsReadmeText = `# OrbStack file sharing

When OrbStack is running, this folder contains Docker volumes and Linux machines. All Docker and Linux files can be found here.

This folder is empty when OrbStack is not running. Do not put files here.

For more details, see:
    - https://docs.orbstack.dev/readme-link/docker-mount
    - https://docs.orbstack.dev/readme-link/machine-mount


## Docker

OrbStack uses standard Docker named volumes.

Create a volume: ` + "`" + `docker volume create foo` + "`" + `
Mount into a container: ` + "`" + `docker run -v foo:/bar ...` + "`" + `
    - Use the volume name to mount it. DO NOT use ~/OrbStack here!
See files from Mac: ` + "`" + `open ~/OrbStack/docker/volumes/foo` + "`" + `

Learn more: https://docs.orbstack.dev/readme-link/docker-mount


---

[OrbStack is NOT running. Files are NOT available.]
`
)

type HcontrolServer struct {
	n         *vnet.Network
	drmClient *drm.DrmClient

	fsnotifyMu   sync.Mutex
	fsnotifyRefs map[string]int
	FsNotifier   *fsnotify.VmNotifier

	NfsPort    int
	nfsMounted bool

	dataFsReady syncx.CondBool
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

func (h *HcontrolServer) GetSSHAuthorizedKeys(_ None, reply *string) error {
	customKey, err := os.ReadFile(conf.ExtraSshDir() + "/id_ed25519.pub")
	if err != nil {
		return err
	}

	authorizedKeys, err := os.ReadFile(conf.ExtraSshDir() + "/authorized_keys")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// if it doesn't exist, create base one
			err = os.WriteFile(conf.ExtraSshDir()+"/authorized_keys", customKey, 0644)
			if err != nil {
				return err
			}
		} else {
			// otherwise that's fine, just log
			logrus.WithError(err).Warn("failed to read authorized_keys")
		}
	}

	// concat base key with authorized
	*reply = strings.TrimSpace(string(customKey) + "\n" + string(authorizedKeys))
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

func (h *HcontrolServer) OnNfsReady(_ None, _ *None) error {
	if h.nfsMounted {
		return nil
	}

	// prep: create nfs dir, write readme, make read-only
	dir := coredir.NfsMountpoint()
	// only if not mounted yet
	if !nfsmnt.IsMountpoint(dir) {
		// coredir.NfsMountpoint() already calls mkdir
		err := os.WriteFile(dir+"/README.txt", []byte(nfsReadmeText), 0644)
		// permission error is normal, that means it's already read only
		if err != nil && !errors.Is(err, os.ErrPermission) {
			logrus.WithError(err).Error("failed to write NFS readme")
		}
		err = os.Chmod(dir, 0555)
		if err != nil {
			logrus.WithError(err).Error("failed to chmod NFS dir")
		}
	}

	if h.NfsPort == 0 {
		return errors.New("nfs port forward not available")
	}

	logrus.Info("Mounting NFS...")
	err := nfsmnt.MountNfs(h.NfsPort)
	if err != nil {
		// if already mounted, we'll just reuse it
		// careful, this could hang
		if nfsmnt.IsMountpoint(dir) {
			logrus.Info("NFS already mounted")
			h.nfsMounted = true
			return nil
		}

		logrus.WithError(err).Error("NFS mount failed")
		return err
	}

	logrus.Info("NFS mounted")
	h.nfsMounted = true
	return nil
}

func (h *HcontrolServer) OnDataFsReady(_ None, _ *None) error {
	logrus.Info("Data FS ready")
	h.dataFsReady.Set(true)
	return nil
}

func (h *HcontrolServer) InternalUnmountNfs() error {
	if !h.nfsMounted {
		return nil
	}

	logrus.Info("Unmounting NFS...")
	err := nfsmnt.UnmountNfs()
	if err != nil {
		logrus.WithError(err).Error("NFS unmount failed")
		return err
	}

	logrus.Info("NFS unmounted")
	h.nfsMounted = false
	return nil
}

func (h *HcontrolServer) InternalWaitDataFsReady() {
	h.dataFsReady.Wait()
}

type None struct{}

func ListenHcontrol(n *vnet.Network, address tcpip.Address) (*HcontrolServer, error) {
	server := &HcontrolServer{
		n:            n,
		drmClient:    drm.Client(),
		fsnotifyRefs: make(map[string]int),
		dataFsReady:  syncx.NewCondBool(),
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
