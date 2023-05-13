package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"reflect"
	"strconv"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

type SconServer struct {
	m *ConManager
}

// Never obfuscate the SconServer type (garble)
var _ = reflect.TypeOf(SconServer{})

func (s *SconServer) Ping(ctx context.Context) error {
	return nil
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
		return nil, fmt.Errorf("create '%s': %w", req.Name, err)
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

func (s *SconServer) GetByID(ctx context.Context, req types.GetByIDRequest) (*types.ContainerRecord, error) {
	c, ok := s.m.GetByID(req.ID)
	if !ok {
		return nil, errors.New("machine not found")
	}

	return c.toRecord(), nil
}

func (s *SconServer) GetByName(ctx context.Context, req types.GetByNameRequest) (*types.ContainerRecord, error) {
	c, ok := s.m.GetByName(req.Name)
	if !ok {
		return nil, errors.New("machine not found")
	}

	return c.toRecord(), nil
}

func (s *SconServer) GetDefaultContainer(ctx context.Context) (*types.ContainerRecord, error) {
	c, err := s.m.GetDefaultContainer()
	if err != nil {
		return nil, err
	}

	return c.toRecord(), nil
}

func (s *SconServer) SetDefaultContainer(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("machine not found")
	}

	return s.m.SetDefaultContainer(c)
}

func (s *SconServer) ClearDefaultContainer(ctx context.Context) error {
	return s.m.SetDefaultContainer(nil)
}

func (s *SconServer) HasExplicitDefaultContainer(ctx context.Context) (bool, error) {
	return s.m.HasExplicitDefaultContainer()
}

func (s *SconServer) ContainerStart(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("machine not found")
	}

	return c.Start()
}

func (s *SconServer) ContainerStop(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("machine not found")
	}

	return c.Stop()
}

func (s *SconServer) ContainerRestart(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("machine not found")
	}

	return c.Restart()
}

func (s *SconServer) ContainerDelete(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("machine not found")
	}

	return c.Delete()
}

func (s *SconServer) ContainerFreeze(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("machine not found")
	}

	return c.Freeze()
}

func (s *SconServer) ContainerUnfreeze(ctx context.Context, record types.ContainerRecord) error {
	c, ok := s.m.GetByID(record.ID)
	if !ok {
		return errors.New("machine not found")
	}

	return c.Unfreeze()
}

func (s *SconServer) ContainerGetLogs(ctx context.Context, req types.ContainerGetLogsRequest) (string, error) {
	c, ok := s.m.GetByID(req.Container.ID)
	if !ok {
		return "", errors.New("machine not found")
	}

	return c.GetLogs(req.Type)
}

func (s *SconServer) InternalReportStopped(ctx context.Context, req types.InternalReportStoppedRequest) error {
	// lxc.Stop() blocks until hook exits, so this breaks the deadlock
	go func() {
		c, ok := s.m.GetByID(req.ID)
		if !ok {
			logrus.WithField("container", req.ID).Error("internal report: container not found")
			return
		}

		err := c.refreshState()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to refresh container state")
		}
	}()
	return nil
}

func (s *SconServer) ShutdownVM(ctx context.Context) error {
	s.m.pendingVMShutdown = true
	return s.m.Close()
}

func (s *SconServer) Serve() error {
	bridge := jhttp.NewBridge(handler.Map{
		"Ping":                        handler.New(s.Ping),
		"Create":                      handler.New(s.Create),
		"ListContainers":              handler.New(s.ListContainers),
		"GetByID":                     handler.New(s.GetByID),
		"GetByName":                   handler.New(s.GetByName),
		"GetDefaultContainer":         handler.New(s.GetDefaultContainer),
		"SetDefaultContainer":         handler.New(s.SetDefaultContainer),
		"ClearDefaultContainer":       handler.New(s.ClearDefaultContainer),
		"HasExplicitDefaultContainer": handler.New(s.HasExplicitDefaultContainer),
		"ContainerStart":              handler.New(s.ContainerStart),
		"ContainerStop":               handler.New(s.ContainerStop),
		"ContainerRestart":            handler.New(s.ContainerRestart),
		"ContainerDelete":             handler.New(s.ContainerDelete),
		"ContainerFreeze":             handler.New(s.ContainerFreeze),
		"ContainerUnfreeze":           handler.New(s.ContainerUnfreeze),
		"ContainerGetLogs":            handler.New(s.ContainerGetLogs),
		"InternalReportStopped":       handler.New(s.InternalReportStopped),
		"ShutdownVM":                  handler.New(s.ShutdownVM),
	}, &jhttp.BridgeOptions{
		Server: &jrpc2.ServerOptions{
			// concurrency limit can cause deadlock in parallel start/stop/create because of post-stop hook reporting
			Concurrency: math.MaxInt,
		},
	})
	defer bridge.Close()

	listenIP := util.DefaultAddress4()
	listenAddrPort := net.JoinHostPort(listenIP.String(), strconv.Itoa(ports.GuestScon))
	return http.ListenAndServe(listenAddrPort, bridge)
}

func runSconServer(m *ConManager) error {
	s := &SconServer{m: m}
	return s.Serve()
}
