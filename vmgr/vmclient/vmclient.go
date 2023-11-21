package vmclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/flock"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"golang.org/x/sys/unix"
)

const (
	forceStopTimeout    = 15 * time.Second
	gracefulStopTimeout = 25 * time.Second
)

var (
	noResult interface{}
)

type VmClient struct {
	rpc *jrpc2.Client
}

func checkCLI(err error) {
	if err != nil {
		//TODO user friendly log
		panic(err)
	}
}

var Client = sync.OnceValue(func() *VmClient {
	err := EnsureVM()
	checkCLI(err)

	client, err := NewClient()
	checkCLI(err)

	return client
})

func NewClient() (*VmClient, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns: 2,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", conf.VmControlSocket())
			},
		},
	}

	ch := jhttp.NewChannel("http://vmrpc", &jhttp.ChannelOptions{
		Client: httpClient,
	})
	rpcClient := jrpc2.NewClient(ch, nil)
	return &VmClient{
		rpc: rpcClient,
	}, nil
}

func (c *VmClient) Close() error {
	return c.rpc.Close()
}

func (c *VmClient) Ping() error {
	return c.rpc.CallResult(context.TODO(), "Ping", nil, &noResult)
}

func (c *VmClient) Stop() error {
	// we only enforce total graceful stop timeout. vmgr has a timeout to switch to force stop
	ctx, cancel := context.WithTimeout(context.Background(), gracefulStopTimeout)
	defer cancel()

	err := c.rpc.CallResult(ctx, "Stop", nil, &noResult)
	// EOF is ok, it means we got disconnected
	// TODO fix
	if err != nil && err.Error() != `[-32603] Post "http://vmrpc": EOF` {
		return err
	}

	return nil
}

func (c *VmClient) SyntheticStopOrKill() error {
	err := c.Stop()
	if err != nil {
		return c.SyntheticKill()
	}

	return nil
}

func (c *VmClient) ForceStop() error {
	ctx, cancel := context.WithTimeout(context.Background(), forceStopTimeout)
	defer cancel()

	err := c.rpc.CallResult(ctx, "ForceStop", nil, &noResult)
	// EOF is ok, it means we got disconnected
	// TODO fix
	if err != nil && err.Error() != `[-32603] Post "http://vmrpc": EOF` {
		return err
	}

	return nil
}

func (c *VmClient) SyntheticForceStopOrKill() error {
	err := c.ForceStop()
	if err != nil {
		return c.SyntheticKill()
	}

	return nil
}

func (c *VmClient) SyntheticKill() error {
	// read pid
	pid, err := flock.ReadPid(conf.VmgrLockFile())
	if err != nil {
		return err
	}

	// safeguard: never kill pid -1 (if lock type is wrong)
	if pid == -1 {
		return fmt.Errorf("invalid pid -1")
	}

	if pid == 0 {
		// nothing to kill
		return nil
	}

	// kill
	err = unix.Kill(pid, unix.SIGKILL)
	if err != nil {
		return err
	}

	return nil
}

func (c *VmClient) ResetData() error {
	return c.rpc.CallResult(context.TODO(), "ResetData", nil, &noResult)
}

func (c *VmClient) GetConfig() (*vmconfig.VmConfig, error) {
	var config vmconfig.VmConfig
	err := c.rpc.CallResult(context.TODO(), "GetConfig", nil, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func (c *VmClient) SetConfig(patch *vmconfig.VmConfig) error {
	return c.rpc.CallResult(context.TODO(), "SetConfig", patch, &noResult)
}

func (c *VmClient) ResetConfig() error {
	return c.rpc.CallResult(context.TODO(), "ResetConfig", nil, &noResult)
}

func (c *VmClient) StartSetup() (*vmtypes.SetupInfo, error) {
	var info vmtypes.SetupInfo
	err := c.rpc.CallResult(context.TODO(), "StartSetup", nil, &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

func (c *VmClient) SetDockerContext() error {
	return c.rpc.CallResult(context.TODO(), "SetDockerContext", nil, &noResult)
}

func (c *VmClient) DockerContainerList() ([]dockertypes.ContainerSummary, error) {
	var containers []dockertypes.ContainerSummary
	err := c.rpc.CallResult(context.TODO(), "DockerContainerList", nil, &containers)
	if err != nil {
		return nil, err
	}

	return containers, nil
}

func (c *VmClient) IsSshConfigWritable() (bool, error) {
	var writable bool
	err := c.rpc.CallResult(context.TODO(), "IsSshConfigWritable", nil, &writable)
	if err != nil {
		return false, err
	}

	return writable, nil
}

func (c *VmClient) InternalReportEnv(report *vmtypes.EnvReport) error {
	return c.rpc.CallResult(context.TODO(), "InternalReportEnv", report, &noResult)
}

func (c *VmClient) InternalSetDockerRemoteCtxAddr(addr string) error {
	return c.rpc.CallResult(context.TODO(), "InternalSetDockerRemoteCtxAddr", &vmtypes.InternalSetDockerRemoteCtxAddrRequest{
		Addr: addr,
	}, &noResult)
}

func (c *VmClient) InternalUpdateToken(token string) error {
	return c.rpc.CallResult(context.TODO(), "InternalUpdateToken", &vmtypes.InternalUpdateTokenRequest{
		RefreshToken: token,
	}, &noResult)
}

func (c *VmClient) InternalDumpDebugInfo() (*vmtypes.DebugInfo, error) {
	var info vmtypes.DebugInfo
	err := c.rpc.CallResult(context.TODO(), "InternalDumpDebugInfo", nil, &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}
