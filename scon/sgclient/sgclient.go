package sgclient

import (
	"net"
	"net/rpc"
)

type None struct{}

type Client struct {
	rpc *rpc.Client
}

type DockerBridgeConfig struct {
	IP4Host string
	IP4Mask string

	IP6Host string
	IP6Mask string

	GuestInterfaceName string
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

func (c *Client) DockerAddNetworkBridge(config DockerBridgeConfig) error {
	var noResult None
	return c.rpc.Call("scg.DockerAddNetworkBridge", config, &noResult)
}
