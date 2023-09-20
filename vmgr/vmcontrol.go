package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alessio/shellescape"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/syssetup"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/orbstack/macvirt/vmgr/vclient"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vnet"
	hcsrv "github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	_ "net/http/pprof"
)

const (
	pprofExtra       = false
	initSetupTimeout = 10 * time.Second
	envReportTimeout = 10 * time.Second
)

type VmControlServer struct {
	vm               *vzf.Machine
	vc               *vclient.VClient
	doneCh           chan struct{}
	stopCh           chan<- types.StopRequest
	pendingResetData bool

	dockerClient *dockerclient.Client
	drm          *drm.DrmClient
	network      *vnet.Network
	hcontrol     *hcsrv.HcontrolServer

	setupDone            bool
	setupMu              sync.Mutex
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

func (s *VmControlServer) GetConfig(ctx context.Context) (*vmconfig.VmConfig, error) {
	return vmconfig.Get(), nil
}

func (s *VmControlServer) SetConfig(ctx context.Context, newConfig *vmconfig.VmConfig) error {
	return vmconfig.Update(func(c *vmconfig.VmConfig) {
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
	return s.dockerClient.Call("POST", "/containers/"+req.ID+"/start", nil, nil)
}

func (s *VmControlServer) DockerContainerStop(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.Call("POST", "/containers/"+req.ID+"/stop", nil, nil)
}

func (s *VmControlServer) DockerContainerKill(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.Call("POST", "/containers/"+req.ID+"/kill", nil, nil)
}

func (s *VmControlServer) DockerContainerRestart(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.Call("POST", "/containers/"+req.ID+"/restart", nil, nil)
}

func (s *VmControlServer) DockerContainerPause(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.Call("POST", "/containers/"+req.ID+"/pause", nil, nil)
}

func (s *VmControlServer) DockerContainerUnpause(ctx context.Context, req vmtypes.IDRequest) error {
	return s.dockerClient.Call("POST", "/containers/"+req.ID+"/unpause", nil, nil)
}

func (s *VmControlServer) DockerContainerDelete(ctx context.Context, params vmtypes.IDRequest) error {
	return s.dockerClient.Call("DELETE", "/containers/"+params.ID+"?force=true", nil, nil)
}

func (s *VmControlServer) DockerVolumeCreate(ctx context.Context, options dockertypes.VolumeCreateOptions) error {
	return s.dockerClient.Call("POST", "/volumes/create", &options, nil)
}

func (s *VmControlServer) DockerVolumeDelete(ctx context.Context, params vmtypes.IDRequest) error {
	return s.dockerClient.Call("DELETE", "/volumes/"+params.ID, nil, nil)
}

func (s *VmControlServer) DockerImageDelete(ctx context.Context, params vmtypes.IDRequest) error {
	return s.dockerClient.Call("DELETE", "/images/"+params.ID+"?force=true", nil, nil)
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
	s.uiEventDebounce.Trigger()
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
	shellCmd := `sh -c ` + shellescape.Quote(shellescape.QuoteCommand([]string{exePath, "report-env"}))
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
	settings, err := vzf.SwextDefaultsGetUserSettings()
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

	return dockerclient.NewWithHTTP(httpClient, nil)
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

		"DockerContainerStart":   handler.New(s.DockerContainerStart),
		"DockerContainerStop":    handler.New(s.DockerContainerStop),
		"DockerContainerKill":    handler.New(s.DockerContainerKill),
		"DockerContainerRestart": handler.New(s.DockerContainerRestart),
		"DockerContainerPause":   handler.New(s.DockerContainerPause),
		"DockerContainerUnpause": handler.New(s.DockerContainerUnpause),
		"DockerContainerDelete":  handler.New(s.DockerContainerDelete),

		"DockerVolumeCreate": handler.New(s.DockerVolumeCreate),
		"DockerVolumeDelete": handler.New(s.DockerVolumeDelete),

		"DockerImageDelete": handler.New(s.DockerImageDelete),

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

	listenerUnix, err := net.Listen("unix", conf.VmControlSocket())
	if err != nil {
		return nil, fmt.Errorf("listen vmcontrol: %w", err)
	}
	go func() { _ = server.Serve(listenerUnix) }()

	// send new configs to GUI
	go func() {
		for range vmconfig.SubscribeDiff() {
			s.uiEventDebounce.Trigger()
		}
	}()

	return func() error {
		// to prevent race, leave open conns open until process exit, like flock
		// just close listeners. Go already sets SO_REUSEADDR
		_ = listenerUnix.Close()
		return nil
	}, nil
}
