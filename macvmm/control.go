package main

import (
	"encoding/json"
	"net/http"

	"github.com/kdrag0n/vz-macvirt/v3"
)

type HostControlServer struct {
	balloon *vz.VirtioMemoryBalloonDevice
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

	server := &http.Server{
		Addr:    "127.0.0.1:3333",
		Handler: mux,
	}

	go server.ListenAndServe()
	return server, nil
}
