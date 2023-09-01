package hcsrv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/muja/goconfig"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/conf/nfsmnt"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/fsnotify"
	"github.com/orbstack/macvirt/vmgr/guihelper"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	vmgrsyncx "github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol/htypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/sshagent"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

const nfsUnmountTimeout = 10 * time.Second

const k8sUIEventDebounce = 250 * time.Millisecond

const (
	nfsReadmeText = `# OrbStack file sharing

When OrbStack is running, this folder contains Docker volumes and Linux machines. All Docker and Linux files can be found here.

This folder is empty when OrbStack is not running. Do not put files here.

For more details, see:
    - https://go.orbstack.dev/docker-mount
    - https://go.orbstack.dev/machine-mount


## Docker

OrbStack uses standard Docker named volumes.

Create a volume: ` + "`" + `docker volume create foo` + "`" + `
Mount into a container: ` + "`" + `docker run -v foo:/bar ...` + "`" + `
    - Use the volume name to mount it. DO NOT use ~/OrbStack here!
See files from Mac: ` + "`" + `open ~/OrbStack/docker/volumes/foo` + "`" + `

Learn more: https://go.orbstack.dev/docker-mount


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

	k8sMu             sync.Mutex
	k8sClient         *kubernetes.Clientset
	k8sNotifyDebounce *vmgrsyncx.LeadingFuncDebounce
	k8sInformerStopCh chan struct{}
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

func (h *HcontrolServer) GetDockerMachineConfig(_ None, reply *htypes.DockerMachineConfig) error {
	cfg := vmconfig.Get()
	*reply = htypes.DockerMachineConfig{
		K8sEnable:         cfg.K8sEnable,
		K8sExposeServices: cfg.K8sExposeServices,
	}

	data, err := os.ReadFile(conf.DockerDaemonConfig())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// write an empty config for user convenience if it doesn't exist
			err = os.WriteFile(conf.DockerDaemonConfig(), []byte("{}"), 0644)
			if err != nil {
				return err
			}

			return nil
		}

		return err
	}

	reply.DockerDaemonConfig = string(data)
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

func (h *HcontrolServer) AddDockerBridge(config sgtypes.DockerBridgeConfig, reply *int) error {
	index, err := h.n.AddVlanBridge(config)
	if err != nil {
		return err
	}

	*reply = index
	return nil
}

func (h *HcontrolServer) RemoveDockerBridge(config sgtypes.DockerBridgeConfig, reply *int) error {
	index, err := h.n.RemoveVlanBridge(config)
	if err != nil {
		return err
	}

	*reply = index
	return nil
}

func (h *HcontrolServer) ClearDockerState(async bool, _ *None) error {
	// fsnotify folder refs
	err := h.clearFsnotifyRefs()
	if err != nil {
		return err
	}

	// vlan router bridge interfaces
	// vmnet is slow (250 ms per bridge!) so do async if manager is stopping
	if async {
		go func() {
			// if stopping then we also know scon bridge will be closed
			err := h.n.ClearVlanBridges(true /*includeScon*/)
			if err != nil {
				logrus.WithError(err).Error("failed to clear docker bridges before stop")
			}
		}()
	} else {
		err = h.n.ClearVlanBridges(false /*includeScon*/)
		if err != nil {
			return err
		}
	}

	// stopping docker machine means k8s also stopped, so stop k8s informer
	h.k8sMu.Lock()
	defer h.k8sMu.Unlock()

	if h.k8sInformerStopCh != nil {
		close(h.k8sInformerStopCh)
		h.k8sInformerStopCh = nil
	}
	h.k8sNotifyDebounce = nil

	// and clear gui state because k8s is push-only to UI
	vzf.SwextIpcNotifyUIEvent(uitypes.UIEvent{
		K8s: &uitypes.K8sEvent{
			CurrentPods:     nil,
			CurrentServices: nil,
		},
	})

	return nil
}

func (h *HcontrolServer) clearFsnotifyRefs() error {
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
	// notify GUI
	vzf.SwextIpcNotifyUIEvent(uitypes.UIEvent{
		Docker: &event,
	})
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

type jsonObject map[string]any

func firstObj(o any) jsonObject {
	if arr, ok := o.([]any); ok {
		if len(arr) > 0 {
			if obj, ok := arr[0].(jsonObject); ok {
				return obj
			}
		}
	}
	return nil
}

func toArr(o any) []any {
	if arr, ok := o.([]any); ok {
		return arr
	}
	return nil
}

func (h *HcontrolServer) OnK8sConfigReady(kubeConfigStr string, _ *None) error {
	logrus.Info("K8s config ready")
	logrus.WithField("kubeConfigStr", kubeConfigStr).Debug("received k8s config")

	// replace k3s "default" with "orbstack"
	regex := regexp.MustCompile(`\bdefault\b`)
	kubeConfigStr = regex.ReplaceAllString(kubeConfigStr, conf.K8sContext)

	// merge with existing config if there is one
	var mergedConfig jsonObject
	// decode our new one as a base first, in case there is no existing config
	err := yaml.Unmarshal([]byte(kubeConfigStr), &mergedConfig)
	if err != nil {
		return fmt.Errorf("parse new config: %w", err)
	}
	// ... and save its new values
	newCluster := firstObj(mergedConfig["clusters"])
	newContext := firstObj(mergedConfig["contexts"])
	newUser := firstObj(mergedConfig["users"])

	// add existing config
	if oldConfigStr, err := os.ReadFile(conf.KubeConfigFile()); err == nil {
		err := yaml.Unmarshal(oldConfigStr, &mergedConfig)
		if err != nil {
			return fmt.Errorf("parse old config: %w", err)
		}

		// merge: clusters, contexts, users
		// for each one: delete any existing with same name, then append new
		for _, typeKey := range []string{"clusters", "contexts", "users"} {
			// remove existing
			var newItems []jsonObject
			for _, newItem := range toArr(mergedConfig[typeKey]) {
				if newItem, ok := newItem.(jsonObject); ok {
					if newItem["name"] != newCluster["name"] {
						newItems = append(newItems, newItem)
					}
				}
			}

			// append new
			switch typeKey {
			case "clusters":
				newItems = append(newItems, newCluster)
			case "contexts":
				newItems = append(newItems, newContext)
			case "users":
				newItems = append(newItems, newUser)
			}
			mergedConfig[typeKey] = newItems
		}
	}

	// set current context
	if vmconfig.Get().DockerSetContext {
		mergedConfig["current-context"] = conf.K8sContext
	}

	// encode in kubectl format
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	err = encoder.Encode(mergedConfig)
	if err != nil {
		return fmt.Errorf("encode merged config: %w", err)
	}

	err = os.WriteFile(conf.KubeConfigFile(), buf.Bytes(), 0600)
	if err != nil {
		return err
	}

	// create k8s client proxy for GUI
	k8sConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeConfigStr))
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}

	// disable proxy. this is internal dial
	k8sConfig.Proxy = nil
	// set dialer
	k8sConfig.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return h.n.DialGuestTCP(ctx, ports.GuestK8s)
	}
	k8sConfig.Timeout = 15 * time.Second

	// let k8s lib create the client with correct TLS settings
	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	h.k8sMu.Lock()
	defer h.k8sMu.Unlock()

	// stop existing informer
	if h.k8sInformerStopCh != nil {
		close(h.k8sInformerStopCh)
		h.k8sInformerStopCh = nil
	}
	h.k8sNotifyDebounce = nil

	// 0 = no periodic resync
	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	podInformer := informerFactory.Core().V1().Pods()
	podLister := podInformer.Lister()
	serviceInformer := informerFactory.Core().V1().Services()
	serviceLister := serviceInformer.Lister()

	debounce := vmgrsyncx.NewLeadingFuncDebounce(func() {
		pods, err := podLister.List(labels.Everything())
		if err != nil {
			logrus.WithError(err).Error("failed to list pods")
			return
		}

		services, err := serviceLister.List(labels.Everything())
		if err != nil {
			logrus.WithError(err).Error("failed to list services")
			return
		}

		// don't send empty slices to swift as nil
		if len(pods) == 0 {
			pods = []*v1.Pod{}
		}
		if len(services) == 0 {
			services = []*v1.Service{}
		}

		vzf.SwextIpcNotifyUIEvent(uitypes.UIEvent{
			K8s: &uitypes.K8sEvent{
				CurrentPods:     pods,
				CurrentServices: services,
			},
		})
	}, k8sUIEventDebounce)

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { debounce.Trigger() },
		UpdateFunc: func(oldObj, newObj any) { debounce.Trigger() },
		DeleteFunc: func(obj any) { debounce.Trigger() },
	}
	podInformer.Informer().AddEventHandler(handler)
	serviceInformer.Informer().AddEventHandler(handler)

	// start informers
	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)

	h.k8sClient = clientset
	h.k8sInformerStopCh = stopCh
	h.k8sNotifyDebounce = debounce
	return nil
}

func (h *HcontrolServer) K8sClient() (*kubernetes.Clientset, error) {
	h.k8sMu.Lock()
	defer h.k8sMu.Unlock()

	if h.k8sClient == nil {
		return nil, errors.New("kubernetes not running")
	}

	return h.k8sClient, nil
}

// trigger event on gui start
func (h *HcontrolServer) K8sReportGuiStarted() {
	h.k8sMu.Lock()
	defer h.k8sMu.Unlock()

	if h.k8sNotifyDebounce != nil {
		h.k8sNotifyDebounce.CallNow()
	}
}

func (h *HcontrolServer) InternalUnmountNfs() error {
	if !h.nfsMounted {
		return nil
	}

	logrus.Info("Unmounting NFS...")
	_, err := util.WithTimeout(func() (struct{}, error) {
		return struct{}{}, nfsmnt.UnmountNfs()
	}, nfsUnmountTimeout)
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
		defer listener.Close()

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
