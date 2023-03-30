package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/dockertypes"
	"github.com/kdrag0n/macvirt/macvmgr/drm"
	"github.com/kdrag0n/macvirt/macvmgr/syssetup"
	"github.com/kdrag0n/macvirt/macvmgr/util"
	"github.com/kdrag0n/macvirt/macvmgr/vclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient/vmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/kdrag0n/macvirt/macvmgr/vzf"
	"github.com/sirupsen/logrus"

	_ "net/http/pprof"
)

const (
	runPprof         = false
	initSetupTimeout = 10 * time.Second
)

type VmControlServer struct {
	vm               *vzf.Machine
	vc               *vclient.VClient
	doneCh           chan struct{}
	stopCh           chan StopType
	pendingResetData bool
	dockerClient     *http.Client
	drm              *drm.DrmClient

	setupDone    bool
	setupMu      sync.Mutex
	setupEnvChan chan *vmtypes.EnvReport
}

func (s *VmControlServer) Ping(ctx context.Context) error {
	return nil
}

func (s *VmControlServer) Stop(ctx context.Context) error {
	// signal stop
	s.stopCh <- StopGraceful

	// wait for main loop to exit
	<-s.doneCh
	return nil
}

func (s *VmControlServer) ForceStop(ctx context.Context) error {
	// signal stop
	s.stopCh <- StopForce

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

func (s *VmControlServer) PatchConfig(ctx context.Context, patch *vmconfig.VmConfigPatch) error {
	return vmconfig.Update(func(c *vmconfig.VmConfig) {
		if patch.MemoryMiB != nil {
			c.MemoryMiB = *patch.MemoryMiB
		}
		if patch.Rosetta != nil {
			c.Rosetta = *patch.Rosetta
		}
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

func (s *VmControlServer) FinishSetup(ctx context.Context) error {
	// our docker context setup always works now,
	// so no need to wait for user to do setup
	return nil
}

func (s *VmControlServer) DockerContainerList(ctx context.Context) ([]dockertypes.Container, error) {
	// only includes running
	resp, err := s.dockerClient.Get("http://docker/containers/json?all=true")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("status: " + resp.Status)
	}

	var containers []dockertypes.Container
	err = json.NewDecoder(resp.Body).Decode(&containers)
	if err != nil {
		return nil, err
	}

	return containers, nil
}

func (s *VmControlServer) DockerContainerStart(ctx context.Context, req vmtypes.IDRequest) error {
	resp, err := s.dockerClient.Post("http://docker/containers/"+req.ID+"/start", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 304 { // Not Modified
			return nil
		}

		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (s *VmControlServer) DockerContainerStop(ctx context.Context, req vmtypes.IDRequest) error {
	resp, err := s.dockerClient.Post("http://docker/containers/"+req.ID+"/stop", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 304 { // Not Modified
			return nil
		}

		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (s *VmControlServer) DockerContainerRestart(ctx context.Context, req vmtypes.IDRequest) error {
	resp, err := s.dockerClient.Post("http://docker/containers/"+req.ID+"/restart", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (s *VmControlServer) DockerContainerPause(ctx context.Context, req vmtypes.IDRequest) error {
	resp, err := s.dockerClient.Post("http://docker/containers/"+req.ID+"/pause", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 304 { // Not Modified
			return nil
		}

		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (s *VmControlServer) DockerContainerUnpause(ctx context.Context, req vmtypes.IDRequest) error {
	resp, err := s.dockerClient.Post("http://docker/containers/"+req.ID+"/unpause", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 304 { // Not Modified
			return nil
		}

		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (s *VmControlServer) DockerContainerRemove(ctx context.Context, params vmtypes.IDRequest) error {
	req, err := http.NewRequest("DELETE", "http://docker/containers/"+params.ID+"?force=true", nil)
	if err != nil {
		return err
	}

	resp, err := s.dockerClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (s *VmControlServer) DockerVolumeList(ctx context.Context) (*dockertypes.VolumeListResponse, error) {
	resp, err := s.dockerClient.Get("http://docker/volumes")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("status: " + resp.Status)
	}

	var volumes dockertypes.VolumeListResponse
	err = json.NewDecoder(resp.Body).Decode(&volumes)
	if err != nil {
		return nil, err
	}

	return &volumes, nil
}

func (s *VmControlServer) DockerVolumeCreate(ctx context.Context, options dockertypes.VolumeCreateOptions) error {
	jsonData, err := json.Marshal(options)
	if err != nil {
		return err
	}

	resp, err := s.dockerClient.Post("http://docker/volumes/create", "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (s *VmControlServer) DockerVolumeRemove(ctx context.Context, params vmtypes.IDRequest) error {
	req, err := http.NewRequest("DELETE", "http://docker/volumes/"+params.ID, nil)
	if err != nil {
		return err
	}

	resp, err := s.dockerClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 409 { // Conflict
			return errors.New("volume in use")
		}

		return errors.New("status: " + resp.Status)
	}

	return nil
}

func (h *VmControlServer) IsSshConfigWritable(ctx context.Context) (bool, error) {
	return syssetup.IsSshConfigWritable(), nil
}

func (h *VmControlServer) InternalReportEnv(ctx context.Context, env *vmtypes.EnvReport) error {
	ch := h.setupEnvChan
	if ch == nil {
		return errors.New("no active env report request")
	}

	ch <- env
	return nil
}

func (h *VmControlServer) runWithEnvReport(combinedArgs ...string) (*vmtypes.EnvReport, error) {
	// start setup
	ch := make(chan *vmtypes.EnvReport, 1)
	h.setupEnvChan = ch

	// run command
	_, err := util.Run(combinedArgs...)
	if err != nil {
		return nil, err
	}

	// wait for report
	env := <-ch
	h.setupEnvChan = nil
	return env, nil
}

// func (s *VmControlServer) doPureGoSetup
func (s *VmControlServer) onStart() error {
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

func listenAndServeUnix(addr string, handler http.Handler) (net.Listener, error) {
	listener, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}

	go http.Serve(listener, handler)
	return listener, nil
}

func makeDockerClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", conf.DockerSocket())
			},
			// don't keep idle conns - it prevents freezing
			MaxIdleConns:    1,
			IdleConnTimeout: 1 * time.Second,
		},
	}
}

func (s *VmControlServer) Serve() (net.Listener, error) {
	bridge := jhttp.NewBridge(handler.Map{
		"Ping":                handler.New(s.Ping),
		"Stop":                handler.New(s.Stop),
		"ForceStop":           handler.New(s.ForceStop),
		"ResetData":           handler.New(s.ResetData),
		"GetConfig":           handler.New(s.GetConfig),
		"PatchConfig":         handler.New(s.PatchConfig),
		"ResetConfig":         handler.New(s.ResetConfig),
		"StartSetup":          handler.New(s.StartSetup),
		"FinishSetup":         handler.New(s.FinishSetup),
		"IsSshConfigWritable": handler.New(s.IsSshConfigWritable),
		"InternalReportEnv":   handler.New(s.InternalReportEnv),

		"DockerContainerList":    handler.New(s.DockerContainerList),
		"DockerContainerStart":   handler.New(s.DockerContainerStart),
		"DockerContainerStop":    handler.New(s.DockerContainerStop),
		"DockerContainerRestart": handler.New(s.DockerContainerRestart),
		"DockerContainerPause":   handler.New(s.DockerContainerPause),
		"DockerContainerUnpause": handler.New(s.DockerContainerUnpause),
		"DockerContainerRemove":  handler.New(s.DockerContainerRemove),

		"DockerVolumeList":   handler.New(s.DockerVolumeList),
		"DockerVolumeCreate": handler.New(s.DockerVolumeCreate),
		"DockerVolumeRemove": handler.New(s.DockerVolumeRemove),
	}, &jhttp.BridgeOptions{
		Server: &jrpc2.ServerOptions{
			// concurrency limit can cause deadlock in parallel start/stop/create because of post-stop hook reporting
			Concurrency: math.MaxInt,
		},
	})

	mux := http.NewServeMux()
	mux.Handle("/", bridge)

	if runPprof {
		go func() {
			runtime.SetBlockProfileRate(1)
			runtime.SetMutexProfileFraction(1)
			err := http.ListenAndServe("localhost:6060", nil)
			if err != nil {
				logrus.Error("pprof: ListenAndServe() =", err)
			}
		}()
	}

	go func() {
		err := http.ListenAndServe("127.0.0.1:"+str(ports.HostVmControl), mux)
		if err != nil {
			logrus.WithError(err).Error("listen vmcontrol failed")
		}
	}()

	listener, err := listenAndServeUnix(conf.VmControlSocket(), mux)
	if err != nil {
		return nil, err
	}

	return listener, nil
}
