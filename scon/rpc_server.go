package main

import (
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
)

type SconServer struct {
	m *ConManager
}

type CreateRequest struct {
	Name         string    `json:"name"`
	Image        ImageSpec `json:"image"`
	UserPassword *string   `json:"user_password"`
}

func (s *SconServer) Create(req CreateRequest) (*ContainerRecord, error) {
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

func (s *SconServer) ListContainers() ([]ContainerRecord, error) {
	var records []ContainerRecord
	for _, c := range s.m.ListContainers() {
		records = append(records, *c.toRecord())
	}

	return records, nil
}
func (s *SconServer) GetByID(id string) (*ContainerRecord, error) {
	c, ok := s.m.GetByID(id)
	if !ok {
		return nil, errors.New("container not found")
	}

	return c.toRecord(), nil
}

func (s *SconServer) GetByName(name string) (*ContainerRecord, error) {
	c, ok := s.m.GetByName(name)
	if !ok {
		return nil, errors.New("container not found")
	}

	return c.toRecord(), nil
}

func (s *SconServer) ContainerStart(record ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Start()
}

func (s *SconServer) ContainerStop(record ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Stop()
}

func (s *SconServer) ContainerDelete(record ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Delete()
}

func (s *SconServer) ContainerFreeze(record ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Freeze()
}

func (s *SconServer) ContainerUnfreeze(record ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("container not found")
	}

	return c.Unfreeze()
}

func (s *SconServer) InternalReportStopped(name string) error {
	c, ok := s.m.GetByName(name)
	if !ok {
		return errors.New("container not found")
	}

	return c.refreshState()
}

func (s *SconServer) Serve() error {
	mux := http.NewServeMux()

	listenIP := getDefaultAddress4()
	listenAddrPort := net.JoinHostPort(listenIP.String(), strconv.Itoa(ports.GuestScon))
	return http.ListenAndServe(listenAddrPort, mux)
}

func runSconServer(m *ConManager) error {
	s := &SconServer{m: m}
	return s.Serve()
}
