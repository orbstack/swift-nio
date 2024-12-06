package sgclient

import (
	"net"
	"net/netip"
	"net/rpc"

	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
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

func (c *Client) OnDockerContainersChanged(diff sgtypes.ContainersDiff) error {
	var noResult None
	return c.rpc.Call("scg.OnDockerContainersChanged", diff, &noResult)
}

func (c *Client) OnDockerImagesChanged(diff sgtypes.Diff[*sgtypes.TaggedImage]) error {
	var noResult None
	return c.rpc.Call("scg.OnDockerImagesChanged", diff, &noResult)
}

func (c *Client) OnDockerVolumesChanged(diff sgtypes.Diff[*dockertypes.Volume]) error {
	var noResult None
	return c.rpc.Call("scg.OnDockerVolumesChanged", diff, &noResult)
}

func (c *Client) OnDockerRefsChanged() error {
	var noResult None
	return c.rpc.Call("scg.OnDockerRefsChanged", None{}, &noResult)
}

func (c *Client) GetProxyUpstreamByHost(host string, v4 bool) (netip.Addr, domainproxytypes.Upstream, error) {
	var reply sgtypes.GetProxyUpstreamByHostResponse
	err := c.rpc.Call("scg.GetProxyUpstreamByHost", sgtypes.GetProxyUpstreamByHostArgs{Host: host, V4: v4}, &reply)
	if err != nil {
		return netip.Addr{}, domainproxytypes.Upstream{}, err
	}
	return reply.Addr, reply.Upstream, nil
}

func (c *Client) GetProxyUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error) {
	var reply domainproxytypes.Upstream
	err := c.rpc.Call("scg.GetProxyUpstreamByAddr", addr, &reply)
	if err != nil {
		return domainproxytypes.Upstream{}, err
	}
	return reply, nil
}
