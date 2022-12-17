package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/kdrag0n/vz-macvirt/v3"
)

type HostControlServer struct {
	balloon  *vz.VirtioMemoryBalloonDevice
	netPairs []*os.File
	routerVm *vz.VirtualMachine
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
			log.Println(err)
		}

		println("start")
		s.routerVm = StartRouterVm(s.netPairs)
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:    "127.0.0.1:3333",
		Handler: mux,
	}

	go server.ListenAndServe()
	return server, nil
}
