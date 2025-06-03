package vmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"time"

	"github.com/alessio/shellescape"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/orbstack/macvirt/scon/isclient"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm"
	"github.com/orbstack/macvirt/vmgr/earlyinit"
	"github.com/orbstack/macvirt/vmgr/swext"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/syssetup"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/orbstack/macvirt/vmgr/vclient"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vmm"
	"github.com/orbstack/macvirt/vmgr/vnet"
	hcsrv "github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	_ "net/http/pprof"
)

const (
	pprofExtra       = false
	initSetupTimeout = 5 * time.Second
	envReportTimeout = 10 * time.Second
)

type VmControlServer struct {
	vm               vmm.Machine
	vc               *vclient.VClient
	doneCh           chan struct{}
	stopCh           chan<- types.StopRequest
	pendingResetData bool

	dockerClient *dockerclient.Client
	drm          *drm.DrmClient
	network      *vnet.Network
	hcontrol     *hcsrv.HcontrolServer

	setupDone            bool
	setupMu              syncx.Mutex
	setupEnvChan         atomic.Pointer[chan *vmtypes.EnvReport]
	setupUserDetailsOnce func() (*UserDetails, error)

	uiEventDebounce syncx.LeadingFuncDebounce
}

func (s *VmControlServer) Ping(ctx context.Context) error {
	return nil
}

func (s *VmControlServer) Stop(ctx context.Context) error {
	// signal stop
	s.stopCh <- types.StopRequest{Type: types.StopTypeGraceful, Reason: types.StopReasonAPI}

	// wait for main loop to exit
	<-s.doneCh
	return nil
}

func (s *VmControlServer) ForceStop(ctx context.Context) error {
	// signal stop
	s.stopCh <- types.StopRequest{Type: types.StopTypeForce, Reason: types.StopReasonAPI}

	// wait for main loop to exit
	<-s.doneCh
	return nil
}

func (s *VmControlServer) ResetData(ctx context.Context) error {
	s.pendingResetData = true
	// force stop - don't care about data loss if we're resetting
	return s.ForceStop(ctx)
}

func (s *VmControlServer) GetConfig(ctx context.Context) (*vmtypes.VmConfig, error) {
	return vmconfig.Get(), nil
}

func (s *VmControlServer) SetConfig(ctx context.Context, newConfig *vmtypes.VmConfig) error {
	return vmconfig.Update(func(c *vmtypes.VmConfig) {
		*c = *newConfig
	})
}

func (s *VmControlServer) ResetConfig(ctx context.Context) error {
	return vmconfig.Reset()
}

func (s *VmControlServer) StartSetup(ctx context.Context) (*vmtypes.SetupInfo, error) {
	info, err := s.doHostSetup()
	if err != nil {
		return nil, err
	}

	return info, nil
}

// for post-migration
func (s *VmControlServer) SetDockerContext(ctx context.Context) error {
	return setupDockerContext()
}

func (s *VmControlServer) DockerContainerStart(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.StartContainer(req.ID)
}

func (s *VmControlServer) DockerContainerStop(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.StopContainer(req.ID)
}

func (s *VmControlServer) DockerContainerKill(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.KillContainer(req.ID)
}

func (s *VmControlServer) DockerContainerRestart(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.RestartContainer(req.ID)
}

func (s *VmControlServer) DockerContainerPause(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.PauseContainer(req.ID)
}

func (s *VmControlServer) DockerContainerUnpause(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.UnpauseContainer(req.ID)
}

func (s *VmControlServer) DockerContainerDelete(ctx context.Context, params vmtypes.IDRequest) error {
	return s.dockerClient.DeleteContainer(params.ID, true)
}

func (s *VmControlServer) DockerVolumeCreate(ctx context.Context, options dockertypes.VolumeCreateOptions) error {
	return s.dockerClient.Call("POST", "/volumes/create", &options, nil)
}

func (s *VmControlServer) DockerVolumeDelete(ctx context.Context, params vmtypes.IDRequest) error {
	return s.dockerClient.DeleteVolume(params.ID)
}

func (s *VmControlServer) DockerImageDelete(ctx context.Context, params vmtypes.IDRequest) error {
	return s.dockerClient.DeleteImage(params.ID, true)
}

func (s *VmControlServer) DockerImageImportFromHostPath(ctx context.Context, params vmtypes.DockerImageImportFromHostPathRequest) error {
	reader, err := os.Open(params.HostPath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer reader.Close()

	return s.dockerClient.ImportImage(reader)
}

func (s *VmControlServer) DockerImageExportToHostPath(ctx context.Context, params vmtypes.DockerImageExportToHostPathRequest) error {
	// open output file
	file, err := os.Create(params.HostPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	reader, err := s.dockerClient.ExportImage(params.ImageID)
	if err != nil {
		return err
	}
	defer reader.Close()

	_, err = io.Copy(file, reader)
	if err != nil {
		return fmt.Errorf("copy data: %w", err)
	}

	return nil
}

func (s *VmControlServer) K8sPodDelete(ctx context.Context, params vmtypes.K8sNameRequest) error {
	client, err := s.k8sClient()
	if err != nil {
		return err
	}
	return client.CoreV1().Pods(params.Namespace).Delete(ctx, params.Name, metav1.DeleteOptions{})
}

func (s *VmControlServer) K8sServiceDelete(ctx context.Context, params vmtypes.K8sNameRequest) error {
	client, err := s.k8sClient()
	if err != nil {
		return err
	}
	return client.CoreV1().Services(params.Namespace).Delete(ctx, params.Name, metav1.DeleteOptions{})
}

func (s *VmControlServer) k8sClient() (*kubernetes.Clientset, error) {
	return s.hcontrol.K8sClient()
}

func (s *VmControlServer) GuiReportStarted(ctx context.Context) error {
	s.hcontrol.K8sReportGuiStarted()
	s.uiEventDebounce.Call()
	return nil
}

func (h *VmControlServer) IsSshConfigWritable(ctx context.Context) (bool, error) {
	return syssetup.IsSshConfigWritable(), nil
}

func (h *VmControlServer) InternalReportEnv(ctx context.Context, env *vmtypes.EnvReport) error {
	ch := h.setupEnvChan.Swap(nil)
	if ch == nil {
		return errors.New("no active env report request")
	}

	*ch <- env
	return nil
}

func (h *VmControlServer) InternalSetDockerRemoteCtxAddr(ctx context.Context, req *vmtypes.InternalSetDockerRemoteCtxAddrRequest) error {
	h.network.DockerRemoteCtxForward.SetAddr(req.Addr)
	return nil
}

func (h *VmControlServer) InternalUpdateToken(ctx context.Context, req *vmtypes.InternalUpdateTokenRequest) error {
	return h.drm.UpdateRefreshToken(req.RefreshToken)
}

func (h *VmControlServer) InternalRefreshDrm(ctx context.Context) error {
	_, err := h.drm.KickCheck(true)
	return err
}

func (h *VmControlServer) InternalDumpDebugInfo(ctx context.Context) (*vmtypes.DebugInfo, error) {
	var buf bytes.Buffer
	if earlyinit.AllowProdHeapProfile {
		err := pprof.WriteHeapProfile(&buf)
		if err != nil {
			return nil, err
		}
	}

	// apply XOR for obfuscation
	arr := buf.Bytes()
	for i := 0; i < len(arr); i++ {
		arr[i] ^= 0x5a
	}

	return &vmtypes.DebugInfo{
		HeapProfile: arr,
	}, nil
}

func (h *VmControlServer) InternalGetEnvPATH(ctx context.Context) (string, error) {
	// get from user details
	details, err := h.setupUserDetailsOnce()
	if err != nil {
		return "", err
	}

	return details.EnvPATH, nil
}

func (h *VmControlServer) InternalIsTestMode(ctx context.Context) (bool, error) {
	return coredir.TestMode(), nil
}

func (h *VmControlServer) runEnvReport(shell string, extraArgs ...string) (*vmtypes.EnvReport, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find executable: %w", err)
	}

	// start setup
	ch := make(chan *vmtypes.EnvReport, 1)
	h.setupEnvChan.Store(&ch)
	defer func() { h.setupEnvChan.CompareAndSwap(&ch, nil) }()

	ctx, cancel := context.WithTimeout(context.Background(), envReportTimeout)
	defer cancel()

	// prepare env report command
	shellCmd := shellescape.QuoteCommand([]string{exePath, "report-env"})
	// for zsh, also include ZDOTDIR, which may not necessarily be exported
	if path.Base(shell) == "zsh" {
		shellCmd = "export ZDOTDIR; " + shellCmd
	}

	// run command
	args := []string{shell}
	args = append(args, extraArgs...)
	args = append(args, "-c", shellCmd)
	err = util.RunLoginShell(ctx, args...)
	if err != nil {
		return nil, err
	}

	// wait for report
	select {
	case env := <-ch:
		return env, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("env report timeout")
	}
}

// func (s *VmControlServer) doPureGoSetup
func (s *VmControlServer) onStart() error {
	// successful VM start.
	// open the GUI app in background mode if it's not already running
	err := s.openGuiApp()
	if err != nil {
		logrus.WithError(err).Error("Failed to open GUI app")
	}

	// if setup isn't done in 10 sec, it means we don't have a GUI (or it's broken)
	// for example, when it's CLI only
	// in such cases, run setup ourselves
	go func() {
		time.Sleep(initSetupTimeout)
		if !s.setupDone {
			logrus.Info("Setup not done in time, running setup...")
			info, err := s.StartSetup(context.Background())
			if err != nil {
				logrus.WithError(err).Error("Failed to run setup")
				return
			}

			// complete setup on cli
			err = completeSetupCli(info)
			if err != nil {
				logrus.WithError(err).Error("Failed to complete CLI-only setup")
				return
			}

			logrus.Info("CLI setup complete")
		}
	}()

	return nil
}

func (s *VmControlServer) openGuiApp() error {
	// only open gui if menu bar is enabled
	settings, err := swext.DefaultsGetUserSettings()
	if err != nil {
		return fmt.Errorf("get user settings: %w", err)
	}

	if !settings.ShowMenubarExtra {
		return nil
	}

	logrus.Info("opening GUI app")
	appBundle, err := conf.FindAppBundle()
	if err != nil {
		return fmt.Errorf("find app bundle: %w", err)
	}

	// -g = no foreground
	// do not use -j (hidden). it causes crash in NSToolbar when opening main window from menu bar
	_, err = util.Run("open", "-g", "-a", appBundle, "--args", "--internal-cli-background")
	if err != nil {
		return fmt.Errorf("open app: %w", err)
	}

	return nil
}

func (s *VmControlServer) onStop() error {
	if s.pendingResetData {
		logrus.Info("Deleting all data...")
		err := os.RemoveAll(conf.DataDir())
		if err != nil {
			return err
		}
	}

	return nil
}

func makeDockerClient() *dockerclient.Client {
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", conf.DockerSocket())
			},
			// don't keep idle conns - it prevents freezing
			MaxIdleConns:    1,
			IdleConnTimeout: 1 * time.Second,
		},
	}

	return dockerclient.NewWithHTTP(nil, httpClient, nil)
}

func (s *VmControlServer) Serve() (func() error, error) {
	bridge := jhttp.NewBridge(handler.Map{
		"Ping":        handler.New(s.Ping),
		"Stop":        handler.New(s.Stop),
		"ForceStop":   handler.New(s.ForceStop),
		"ResetData":   handler.New(s.ResetData),
		"GetConfig":   handler.New(s.GetConfig),
		"SetConfig":   handler.New(s.SetConfig),
		"ResetConfig": handler.New(s.ResetConfig),

		"StartSetup":                     handler.New(s.StartSetup),
		"SetDockerContext":               handler.New(s.SetDockerContext),
		"IsSshConfigWritable":            handler.New(s.IsSshConfigWritable),
		"InternalReportEnv":              handler.New(s.InternalReportEnv),
		"InternalSetDockerRemoteCtxAddr": handler.New(s.InternalSetDockerRemoteCtxAddr),
		"InternalUpdateToken":            handler.New(s.InternalUpdateToken),
		"InternalRefreshDrm":             handler.New(s.InternalRefreshDrm),
		"InternalDumpDebugInfo":          handler.New(s.InternalDumpDebugInfo),
		"InternalGetEnvPATH":             handler.New(s.InternalGetEnvPATH),
		"InternalIsTestMode":             handler.New(s.InternalIsTestMode),

		"DockerContainerStart":   handler.New(s.DockerContainerStart),
		"DockerContainerStop":    handler.New(s.DockerContainerStop),
		"DockerContainerKill":    handler.New(s.DockerContainerKill),
		"DockerContainerRestart": handler.New(s.DockerContainerRestart),
		"DockerContainerPause":   handler.New(s.DockerContainerPause),
		"DockerContainerUnpause": handler.New(s.DockerContainerUnpause),
		"DockerContainerDelete":  handler.New(s.DockerContainerDelete),

		"DockerVolumeCreate": handler.New(s.DockerVolumeCreate),
		"DockerVolumeDelete": handler.New(s.DockerVolumeDelete),

		"DockerImageDelete":             handler.New(s.DockerImageDelete),
		"DockerImageImportFromHostPath": handler.New(s.DockerImageImportFromHostPath),
		"DockerImageExportToHostPath":   handler.New(s.DockerImageExportToHostPath),

		"K8sPodDelete":     handler.New(s.K8sPodDelete),
		"K8sServiceDelete": handler.New(s.K8sServiceDelete),

		"GuiReportStarted": handler.New(s.GuiReportStarted),
	}, &jhttp.BridgeOptions{
		Server: &jrpc2.ServerOptions{
			// concurrency limit can cause deadlock in parallel start/stop/create because of post-stop hook reporting
			Concurrency: math.MaxInt,
		},
	})

	mux := http.NewServeMux()
	mux.Handle("/", bridge)

	// pprof server
	if conf.Debug() {
		go func() {
			// affects perf
			if pprofExtra {
				runtime.SetBlockProfileRate(1)
				runtime.SetMutexProfileFraction(1)
			}
			err := http.ListenAndServe("localhost:6060", nil)
			if err != nil {
				logrus.Error("pprof: ListenAndServe() =", err)
			}
		}()
	}

	server := &http.Server{
		Handler: mux,
	}

	listenerUnix, err := netx.ListenUnix(conf.VmControlSocket())
	if err != nil {
		return nil, fmt.Errorf("listen vmcontrol: %w", err)
	}
	go func() { _ = server.Serve(listenerUnix) }()

	// send new configs to GUI
	go func() {
		for range vmconfig.SubscribeDiff() {
			s.uiEventDebounce.Call()
		}
	}()

	// send new configs to scon
	go func() {
		for change := range vmconfig.SubscribeDiff() {
			err := s.drm.UseSconInternalClient(func(scon *isclient.Client) error {
				return scon.OnVmconfigUpdate(change.New)
			})
			if err != nil {
				logrus.WithError(err).Error("failed to send vmconfig update to scon")
			}
		}
	}()

	return func() error {
		// to prevent race, leave open conns open until process exit, like flock
		// just close listeners. Go already sets SO_REUSEADDR
		_ = listenerUnix.Close()
		return nil
	}, nil
}
