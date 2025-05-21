package dockerclient

import (
	"fmt"
	"net/url"

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

func (c *Client) GetVolume(name string) (*dockertypes.Volume, error) {
	var volume dockertypes.Volume
	err := c.Call("GET", "/volumes/"+url.PathEscape(name), nil, &volume)
	if err != nil {
		return nil, fmt.Errorf("get volume: %w", err)
	}
	return &volume, nil
}

func (c *Client) CreateVolume(options dockertypes.VolumeCreateRequest) (dockertypes.Volume, error) {
	var volume dockertypes.Volume
	err := c.Call("POST", "/volumes/create", options, &volume)
	if err != nil {
		return dockertypes.Volume{}, fmt.Errorf("create volume: %w", err)
	}
	return volume, nil
}
