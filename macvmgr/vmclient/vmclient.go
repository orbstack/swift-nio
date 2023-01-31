package vmclient

import (
	"context"
	"net"
	"net/http"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
)

var (
	cachedClient *VmClient
	noResult     interface{}
)

type VmClient struct {
	rpc *jrpc2.Client
}

func Client() *VmClient {
	if cachedClient != nil {
		return cachedClient
	}

	EnsureVM()

	client, err := newClient()
	if err != nil {
		panic(err)
	}

	cachedClient = client
	return client
}

func newClient() (*VmClient, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns: 5,
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
	return c.rpc.CallResult(context.TODO(), "Stop", nil, &noResult)
}

func (c *VmClient) ForceStop() error {
	return c.rpc.CallResult(context.TODO(), "ForceStop", nil, &noResult)
}

func (c *VmClient) ResetData() error {
	return c.rpc.CallResult(context.TODO(), "ResetData", nil, &noResult)
}

func (c *VmClient) PatchConfig(patch *vmconfig.VmConfig) error {
	return c.rpc.CallResult(context.TODO(), "PatchConfig", patch, &noResult)
}
