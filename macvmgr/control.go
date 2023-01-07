package main

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/vclient"
	"github.com/sirupsen/logrus"

	_ "net/http/pprof"
)

const (
	runPprof           = false
	memoryRampStep     = 200 * 1024 * 1024 // 200 MB
	memoryRampInterval = 1 * time.Second
)

type HostControlServer struct {
	balloon        *vz.VirtioMemoryBalloonDevice
	netPair2       *os.File
	routerVm       *vz.VirtualMachine
	vc             *vclient.VClient
	lastMemorySize int64
}

type SetBalloonRequest struct {
	Target int64 `json:"target"`
}

func min[T int64 | uint64](a, b T) T {
	if a < b {
		return a
	}
	return b
}
func max[T int64 | uint64](a, b T) T {
	if a > b {
		return a
	}
	return b
}
func abs[T int64 | uint64](a T) T {
	if a < 0 {
		return -a
	}
	return a
}
func sign[T int64](a T) T {
	if a < 0 {
		return -1
	}
	return 1
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

		// ramp balloon
		targetSize := req.Target * 1024 * 1024
		for s.lastMemorySize != targetSize {
			targetDelta := targetSize - s.lastMemorySize
			delta := min(memoryRampStep, abs(targetDelta)) * sign(targetDelta)

			s.lastMemorySize += delta
			logrus.Debug("Set memory: ", s.lastMemorySize/1024/1024, " MiB  - delta: ", delta/1024/1024, " MiB")
			s.balloon.SetTargetVirtualMachineMemorySize(uint64(s.lastMemorySize))
			time.Sleep(memoryRampInterval)
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/reboot_router", func(w http.ResponseWriter, r *http.Request) {
		println("stop")
		err := s.routerVm.Stop()
		if err != nil {
			logrus.Error("router vm stop", err)
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
				logrus.Error("pprof: ListenAndServe() =", err)
			}
		}()
	}

	go server.ListenAndServe()
	return server, nil
}
