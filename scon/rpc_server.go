package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/types"
)

type SconServer struct {
	m *ConManager
}

func (s *SconServer) Create(ctx context.Context, req types.CreateRequest) (*types.ContainerRecord, error) {
	pwd := ""
	if req.UserPassword != nil {
		pwd = *req.UserPassword
	}
	c, err := s.m.Create(CreateParams{
		Name:         req.Name,
		Image:        req.Image,
		UserPassword: pwd,
	})
	if err != nil {
		return nil, err
	}

	return c.toRecord(), nil
}

func (s *SconServer) ListContainers(ctx context.Context) ([]types.ContainerRecord, error) {
	var records []types.ContainerRecord
	for _, c := range s.m.ListContainers() {
		records = append(records, *c.toRecord())
	}

	return records, nil
}

func (s *SconServer) GetByID(ctx context.Context, id string) (*types.ContainerRecord, error) {
	c, ok := s.m.GetByID(id)
	if !ok {
		return nil, errors.New("container not found")
	}

	return c.toRecord(), nil
}

func (s *SconServer) GetByName(ctx context.Context, name string) (*types.ContainerRecord, error) {
	c, ok := s.m.GetByName(name)
	if !ok {
		return nil, errors.New("container not found")
	}

	return c.toRecord(), nil
}

func (s *SconServer) ContainerStart(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Start()
}

func (s *SconServer) ContainerStop(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Stop()
}

func (s *SconServer) ContainerDelete(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Delete()
}

func (s *SconServer) ContainerFreeze(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Freeze()
}

func (s *SconServer) ContainerUnfreeze(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Unfreeze()
}

func (s *SconServer) InternalReportStopped(ctx context.Context, id string) error {
	c, ok := s.m.GetByID(id)
	if !ok {
		return errors.New("container not found")
	}

	return c.refreshState()
}

func (s *SconServer) Serve() error {
	bridge := jhttp.NewBridge(handler.Map{
		"Create":                handler.New(s.Create),
		"ListContainers":        handler.New(s.ListContainers),
		"GetByID":               handler.New(s.GetByID),
		"GetByName":             handler.New(s.GetByName),
		"ContainerStart":        handler.New(s.ContainerStart),
		"ContainerStop":         handler.New(s.ContainerStop),
		"ContainerDelete":       handler.New(s.ContainerDelete),
		"ContainerFreeze":       handler.New(s.ContainerFreeze),
		"ContainerUnfreeze":     handler.New(s.ContainerUnfreeze),
		"InternalReportStopped": handler.New(s.InternalReportStopped),
	}, nil)
	defer bridge.Close()

	mux := http.NewServeMux()
	mux.Handle("/", bridge)

	listenIP := getDefaultAddress4()
	listenAddrPort := net.JoinHostPort(listenIP.String(), strconv.Itoa(ports.GuestScon))
	return http.ListenAndServe(listenAddrPort, mux)
}

func runSconServer(m *ConManager) error {
	s := &SconServer{m: m}
	return s.Serve()
}
