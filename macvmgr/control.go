package main

import (
	"net"
	"net/http"
	"os"
	"runtime"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vclient"
	"github.com/sirupsen/logrus"

	_ "net/http/pprof"
)

const (
	runPprof = false
)

type VmControlServer struct {
	balloon  *vz.VirtioMemoryBalloonDevice
	netPair2 *os.File
	routerVm *vz.VirtualMachine
	vc       *vclient.VClient
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
	mux := http.NewServeMux()

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
