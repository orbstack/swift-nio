package main

import (
	"net"
	"net/http"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
)

type SconRpcServer struct {
	m *ConManager
}

func (s *SconRpcServer) Serve() error {
	mux := http.NewServeMux()

	listenIP := getDefaultAddress4()
	listenAddrPort := net.JoinHostPort(listenIP.String(), strconv.Itoa(ports.GuestScon))
	return http.ListenAndServe(listenAddrPort, mux)
}

func runSconServer(m *ConManager) error {
	s := &SconRpcServer{m: m}
	return s.Serve()
}
