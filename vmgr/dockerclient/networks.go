package dockerclient

import (
	"fmt"
	"net/url"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

func (c *Client) ListNetworks() ([]dockertypes.Network, error) {
	var networks []dockertypes.Network
	err := c.Call("GET", "/networks", nil, &networks)
	if err != nil {
		return nil, fmt.Errorf("get networks: %w", err)
	}

	return networks, nil
}

func (c *Client) ListNetworksFull() ([]dockertypes.Network, error) {
	var networks []dockertypes.Network
	err := c.Call("GET", "/networks", nil, &networks)
	if err != nil {
		return nil, fmt.Errorf("get networks: %w", err)
	}

	for i := range networks {
		err = c.Call("GET", fmt.Sprintf("/networks/%s", networks[i].ID), nil, &networks[i])
		if err != nil {
			return nil, fmt.Errorf("get network %s: %w", networks[i].ID, err)
		}
	}

	return networks, nil
}

func (c *Client) CreateNetwork(network dockertypes.Network) (dockertypes.NetworkCreateResponse, error) {
	var resp dockertypes.NetworkCreateResponse
	err := c.Call("POST", "/networks/create", network, &resp)
	if err != nil {
		return resp, fmt.Errorf("create network: %w", err)
	}

	return resp, nil
}

func (c *Client) DeleteNetwork(networkID string) error {
	err := c.Call("DELETE", "/networks/"+url.PathEscape(networkID), nil, nil)
	if err != nil {
		return fmt.Errorf("remove network %s: %w", networkID, err)
	}
	return nil
}
