package dockerclient

import (
	"fmt"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

func (c *Client) ListVolumes() ([]*dockertypes.Volume, error) {
	var volumesResp dockertypes.VolumeListResponse
	err := c.Call("GET", "/volumes", nil, &volumesResp)
	if err != nil {
		return nil, fmt.Errorf("get volumes: %w", err)
	}
	volumes := volumesResp.Volumes

	return volumes, nil
}
