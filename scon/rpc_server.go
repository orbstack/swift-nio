package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

type SconServer struct {
	m             *ConManager
	dockerMachine *Container
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
	containers := s.m.ListContainers()
	records := make([]types.ContainerRecord, 0, len(containers))
	for _, c := range containers {
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

	return c.Stop(StopOptions{
		Force: false,
	})
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

func (s *SconServer) ContainerRename(ctx context.Context, req types.ContainerRenameRequest) error {
	if req.Container == nil {
		return errors.New("container is nil")
	}

	c, err := s.m.GetByID(req.Container.ID)
	if err != nil {
		return err
	}

	return c.Rename(req.NewName)
}

func (s *SconServer) ContainerGetLogs(ctx context.Context, req types.ContainerGetLogsRequest) (string, error) {
	if req.Container == nil {
		return "", errors.New("container is nil")
	}

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

func (s *SconServer) InternalDockerMigrationLoadImage(ctx context.Context, req types.InternalDockerMigrationLoadImageRequest) error {
	return s.dockerMachine.UseAgent(func(a *agent.Client) error {
		return a.DockerMigrationLoadImage(req)
	})
}

func (s *SconServer) InternalDockerMigrationRunSyncServer(ctx context.Context, req types.InternalDockerMigrationRunSyncServerRequest) error {
	return s.dockerMachine.UseAgent(func(a *agent.Client) error {
		return a.DockerMigrationRunSyncServer(req)
	})
}

func (s *SconServer) InternalDockerMigrationWaitSync(ctx context.Context, req types.InternalDockerMigrationWaitSyncRequest) error {
	return s.dockerMachine.UseAgent(func(a *agent.Client) error {
		return a.DockerMigrationWaitSync(req)
	})
}

func (s *SconServer) InternalDockerMigrationStopSyncServer(ctx context.Context) error {
	return s.dockerMachine.UseAgent(func(a *agent.Client) error {
		return a.DockerMigrationStopSyncServer()
	})
}

func (s *SconServer) InternalDockerFastDf(ctx context.Context) (*dockertypes.SystemDf, error) {
	return UseAgentRet(s.dockerMachine, func(a *agent.Client) (*dockertypes.SystemDf, error) {
		return a.DockerFastDf()
	})
}

func (s *SconServer) InternalDeleteK8s(ctx context.Context) error {
	return s.dockerMachine.DeleteK8s()
}

func (s *SconServer) InternalGuiReportStarted(ctx context.Context) error {
	s.m.uiEventDebounce.Trigger()

	// try refreshing docker too
	c, err := s.m.GetByID(ContainerIDDocker)
	if err == nil {
		err = c.UseAgent(func(a *agent.Client) error {
			return a.DockerGuiReportStarted()
		})
		if err != nil {
			logrus.WithError(err).Error("failed to report docker gui started")
		}
	}

	return nil
}

func (s *SconServer) Serve() error {
	bridge := jhttp.NewBridge(handler.Map{
		"Ping":                                  handler.New(s.Ping),
		"Create":                                handler.New(s.Create),
		"ListContainers":                        handler.New(s.ListContainers),
		"GetByID":                               handler.New(s.GetByID),
		"GetByName":                             handler.New(s.GetByName),
		"GetDefaultContainer":                   handler.New(s.GetDefaultContainer),
		"SetDefaultContainer":                   handler.New(s.SetDefaultContainer),
		"GetDefaultUsername":                    handler.New(s.GetDefaultUsername),
		"SetDefaultUsername":                    handler.New(s.SetDefaultUsername),
		"ContainerStart":                        handler.New(s.ContainerStart),
		"ContainerStop":                         handler.New(s.ContainerStop),
		"ContainerRestart":                      handler.New(s.ContainerRestart),
		"ContainerDelete":                       handler.New(s.ContainerDelete),
		"ContainerRename":                       handler.New(s.ContainerRename),
		"ContainerGetLogs":                      handler.New(s.ContainerGetLogs),
		"InternalReportStopped":                 handler.New(s.InternalReportStopped),
		"InternalDockerMigrationLoadImage":      handler.New(s.InternalDockerMigrationLoadImage),
		"InternalDockerMigrationRunSyncServer":  handler.New(s.InternalDockerMigrationRunSyncServer),
		"InternalDockerMigrationWaitSync":       handler.New(s.InternalDockerMigrationWaitSync),
		"InternalDockerMigrationStopSyncServer": handler.New(s.InternalDockerMigrationStopSyncServer),
		"InternalDockerFastDf":                  handler.New(s.InternalDockerFastDf),
		// TODO better alias
		"InternalDeleteK8s":        handler.New(s.InternalDeleteK8s),
		"InternalGuiReportStarted": handler.New(s.InternalGuiReportStarted),
	}, &jhttp.BridgeOptions{
		Server: &jrpc2.ServerOptions{
			// concurrency limit can cause deadlock in parallel start/stop/create because of post-stop hook reporting
			Concurrency: math.MaxInt,
		},
	})
	defer bridge.Close()

	listenIP := util.DefaultAddress4()
	listenAddrPort := net.JoinHostPort(listenIP.String(), strconv.Itoa(ports.GuestScon))
	// need to use netx listener to disable keepalive
	listener, err := netx.Listen("tcp", listenAddrPort)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:    listenAddrPort,
		Handler: bridge,
	}
	return server.Serve(listener)
}

func ListenScon(m *ConManager, dockerMachine *Container) error {
	s := &SconServer{
		m:             m,
		dockerMachine: dockerMachine,
	}
	return s.Serve()
}
