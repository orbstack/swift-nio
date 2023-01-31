package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"runtime"

	"github.com/Code-Hex/vz/v3"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/sirupsen/logrus"

	_ "net/http/pprof"
)

const (
	runPprof = false
)

type VmControlServer struct {
	balloon          *vz.VirtioMemoryBalloonDevice
	vm               *vz.VirtualMachine
	vc               *vclient.VClient
	doneCh           chan struct{}
	stopCh           chan StopType
	pendingResetData bool
}

func (s *VmControlServer) Ping(ctx context.Context) error {
	return nil
}

func (s *VmControlServer) Stop(ctx context.Context) error {
	// signal stop
	err := tryGracefulStop(s.vm, s.vc)
	if err != nil {
		return err
	}

	// wait for main loop to exit
	<-s.doneCh
	return nil
}

func (s *VmControlServer) ForceStop(ctx context.Context) error {
	// signal stop
	err := tryForceStop(s.vm)
	if err != nil {
		return err
	}

	// wait for main loop to exit
	<-s.doneCh
	return nil
}

func (s *VmControlServer) ResetData(ctx context.Context) error {
	s.pendingResetData = true
	// force stop - don't care about data loss if we're resetting
	return s.ForceStop(ctx)
}

func (s *VmControlServer) PatchConfig(ctx context.Context, patch *vmconfig.VmConfig) error {
	return vmconfig.Update(func(c *vmconfig.VmConfig) {
		if patch.MemoryMiB != 0 {
			c.MemoryMiB = patch.MemoryMiB
		}
	})
}

func (s *VmControlServer) OnStop(ctx context.Context) error {
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

func (s *VmControlServer) Serve() (net.Listener, error) {
	bridge := jhttp.NewBridge(handler.Map{
		"Ping":        handler.New(s.Ping),
		"Stop":        handler.New(s.Stop),
		"ResetData":   handler.New(s.ResetData),
		"PatchConfig": handler.New(s.PatchConfig),
	}, nil)

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

	go http.ListenAndServe("127.0.0.1"+str(ports.HostVmControl), mux)
	listener, err := listenAndServeUnix(conf.VmControlSocket(), mux)
	if err != nil {
		return nil, err
	}

	return listener, nil
}
