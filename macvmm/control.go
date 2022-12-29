package main

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmm/conf"
	"github.com/kdrag0n/macvirt/macvmm/vclient"
	"go.uber.org/zap"

	_ "net/http/pprof"
)

const (
	runPprof = false
)

type HostControlServer struct {
	balloon  *vz.VirtioMemoryBalloonDevice
	netPair2 *os.File
	routerVm *vz.VirtualMachine
	vc       *vclient.VClient
}

type SetBalloonRequest struct {
	Target uint64 `json:"target"`
}

func (s *HostControlServer) Serve() (*http.Server, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/balloon", func(w http.ResponseWriter, r *http.Request) {
		// parse json
		var req SetBalloonRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// set balloon
		s.balloon.SetTargetVirtualMachineMemorySize(req.Target * 1024 * 1024)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/reboot_router", func(w http.ResponseWriter, r *http.Request) {
		println("stop")
		err := s.routerVm.Stop()
		if err != nil {
			zap.S().Error("router vm stop", err)
		}

		println("start")
		s.routerVm = StartRouterVm(s.netPair2)
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    "127.0.0.1:" + strconv.Itoa(conf.HostPortHcontrol),
		Handler: mux,
	}

	if runPprof {
		go func() {
			runtime.SetBlockProfileRate(1)
			runtime.SetMutexProfileFraction(1)
			err := http.ListenAndServe("localhost:6060", nil)
			if err != nil {
				zap.S().Error("pprof: ListenAndServe() =", err)
			}
		}()
	}

	go server.ListenAndServe()
	return server, nil
}
