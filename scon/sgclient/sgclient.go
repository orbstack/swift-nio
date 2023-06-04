package sgclient

import (
	"net"
	"net/rpc"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
)

type None struct{}

type Client struct {
	rpc *rpc.Client
}

func New(conn net.Conn) (*Client, error) {
	return &Client{
		rpc: rpc.NewClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.rpc.Close()
}

func (c *Client) Ping() error {
	var noResult None
	return c.rpc.Call("scg.Ping", None{}, &noResult)
}

func (c *Client) DockerAddBridge(config sgtypes.DockerBridgeConfig) error {
	var noResult None
	return c.rpc.Call("scg.DockerAddBridge", config, &noResult)
}

func (c *Client) DockerRemoveBridge(config sgtypes.DockerBridgeConfig) error {
	var noResult None
	return c.rpc.Call("scg.DockerRemoveBridge", config, &noResult)
}
