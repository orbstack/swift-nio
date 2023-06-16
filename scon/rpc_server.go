package main

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

type SconServer struct {
	m *ConManager
}

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
	c, err := s.m.GetByID(req.ID)
	if err != nil {
		return nil, err
	}

	return c.toRecord(), nil
}

func (s *SconServer) GetByName(ctx context.Context, req types.GetByNameRequest) (*types.ContainerRecord, error) {
	c, err := s.m.GetByName(req.Name)
	if err != nil {
		return nil, err
	}

	return c.toRecord(), nil
}

func (s *SconServer) GetDefaultContainer(ctx context.Context) (*types.ContainerRecord, error) {
	c, isExplicit, err := s.m.GetDefaultContainer()
	if err != nil {
		return nil, err
	}

	// no explicit default = nil
	if !isExplicit {
		return nil, nil
	}

	return c.toRecord(), nil
}

func (s *SconServer) SetDefaultContainer(ctx context.Context, record *types.ContainerRecord) error {
	if record == nil || record.ID == "" {
		return s.m.SetDefaultContainer(nil)
	}

	c, err := s.m.GetByID(record.ID)
	if err != nil {
		return err
	}

	return s.m.SetDefaultContainer(c)
}

func (s *SconServer) GetDefaultUsername(ctx context.Context) (string, error) {
	return s.m.defaultUser()
}

func (s *SconServer) SetDefaultUsername(ctx context.Context, req types.SetDefaultUsernameRequest) error {
	return s.m.SetDefaultUsername(req.Username)
}

func (s *SconServer) ContainerStart(ctx context.Context, record types.ContainerRecord) error {
	c, err := s.m.GetByID(record.ID)
	if err != nil {
		return err
	}

	return c.Start()
}

func (s *SconServer) ContainerStop(ctx context.Context, record types.ContainerRecord) error {
	c, err := s.m.GetByID(record.ID)
	if err != nil {
		return err
	}

	return c.Stop()
}

func (s *SconServer) ContainerRestart(ctx context.Context, record types.ContainerRecord) error {
	c, err := s.m.GetByID(record.ID)
	if err != nil {
		return err
	}

	return c.Restart()
}

func (s *SconServer) ContainerDelete(ctx context.Context, record types.ContainerRecord) error {
	c, err := s.m.GetByID(record.ID)
	if err != nil {
		return err
	}

	return c.Delete()
}

func (s *SconServer) ContainerGetLogs(ctx context.Context, req types.ContainerGetLogsRequest) (string, error) {
	c, err := s.m.GetByID(req.Container.ID)
	if err != nil {
		return "", err
	}

	return c.GetLogs(req.Type)
}

func (s *SconServer) InternalReportStopped(ctx context.Context, req types.InternalReportStoppedRequest) error {
	// lxc.Stop() blocks until hook exits, so this breaks the deadlock
	go func() {
		c, err := s.m.GetByID(req.ID)
		if err != nil {
			logrus.WithField("container", req.ID).WithError(err).Error("internal report failed")
			return
		}

		err = c.refreshState()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to refresh container state")
		}
	}()
	return nil
}

func (s *SconServer) InternalRefreshDockerNetworks(ctx context.Context, req types.InternalRefreshDockerNetworksRequest) error {
	c, err := s.m.GetByID(ContainerIDDocker)
	if err != nil {
		return err
	}

	// no need to refresh if docker isn't running
	if !c.Running() {
		return nil
	}

	return c.UseAgent(func(a *agent.Client) error {
		return a.DockerHandleConn()
	})
}

func (s *SconServer) ShutdownVM(ctx context.Context) error {
	s.m.pendingVMShutdown = true
	return s.m.Close()
}

func (s *SconServer) Serve() error {
	bridge := jhttp.NewBridge(handler.Map{
		"Ping":                          handler.New(s.Ping),
		"Create":                        handler.New(s.Create),
		"ListContainers":                handler.New(s.ListContainers),
		"GetByID":                       handler.New(s.GetByID),
		"GetByName":                     handler.New(s.GetByName),
		"GetDefaultContainer":           handler.New(s.GetDefaultContainer),
		"SetDefaultContainer":           handler.New(s.SetDefaultContainer),
		"GetDefaultUsername":            handler.New(s.GetDefaultUsername),
		"SetDefaultUsername":            handler.New(s.SetDefaultUsername),
		"ContainerStart":                handler.New(s.ContainerStart),
		"ContainerStop":                 handler.New(s.ContainerStop),
		"ContainerRestart":              handler.New(s.ContainerRestart),
		"ContainerDelete":               handler.New(s.ContainerDelete),
		"ContainerGetLogs":              handler.New(s.ContainerGetLogs),
		"InternalReportStopped":         handler.New(s.InternalReportStopped),
		"InternalRefreshDockerNetworks": handler.New(s.InternalRefreshDockerNetworks),
		"ShutdownVM":                    handler.New(s.ShutdownVM),
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

func ListenScon(m *ConManager) error {
	s := &SconServer{m: m}
	return s.Serve()
}
