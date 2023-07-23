package dockerclient

import (
	"fmt"

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
