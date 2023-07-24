package dockerclient

import (
	"fmt"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

func (c *Client) ListImages() ([]*dockertypes.Image, error) {
	var images []*dockertypes.Image
	err := c.Call("GET", "/images/json?shared-size=1", nil, &images)
	if err != nil {
		return nil, fmt.Errorf("get images: %w", err)
	}

	return images, nil
}
