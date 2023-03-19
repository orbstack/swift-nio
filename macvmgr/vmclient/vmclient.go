package vmclient

import (
	"context"
	"net"
	"net/http"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/dockertypes"
	"github.com/kdrag0n/macvirt/macvmgr/syncx"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient/vmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
)

var (
	onceClient syncx.Once[*VmClient]
	noResult   interface{}
)

type VmClient struct {
	rpc *jrpc2.Client
}

func Client() *VmClient {
	return onceClient.Do(func() *VmClient {
		err := EnsureVM()
		if err != nil {
			panic(err)
		}

		client, err := newClient()
		if err != nil {
			panic(err)
		}

		return client
	})
}

func newClient() (*VmClient, error) {
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
	err := c.rpc.CallResult(context.TODO(), "Stop", nil, &noResult)
	// EOF is ok, it means we got disconnected
	// TODO fix
	if err != nil && err.Error() != `[-32603] Post "http://vmrpc": EOF` {
		return err
	}

	return nil
}

func (c *VmClient) ForceStop() error {
	err := c.rpc.CallResult(context.TODO(), "ForceStop", nil, &noResult)
	// EOF is ok, it means we got disconnected
	// TODO fix
	if err != nil && err.Error() != `[-32603] Post "http://vmrpc": EOF` {
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

func (c *VmClient) PatchConfig(patch *vmconfig.VmConfigPatch) error {
	return c.rpc.CallResult(context.TODO(), "PatchConfig", patch, &noResult)
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

func (c *VmClient) FinishSetup() error {
	return c.rpc.CallResult(context.TODO(), "FinishSetup", nil, &noResult)
}

func (c *VmClient) DockerContainerList() ([]dockertypes.Container, error) {
	var containers []dockertypes.Container
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
