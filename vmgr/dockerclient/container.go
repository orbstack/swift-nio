package dockerclient

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

func splitRepoTag(repoTag string) (string, string) {
	// last index, to deal with "localhost:5000/myimage:latest"
	sepPos := strings.LastIndex(repoTag, ":")
	if sepPos == -1 {
		return repoTag, "latest"
	}

	repoPart := repoTag[:sepPos]
	tagPart := repoTag[sepPos+1:]
	return repoPart, tagPart
}

func (c *Client) RunContainer(req *dockertypes.ContainerCreateRequest) (string, error) {
	// need to pull image first
	repoPart, tagPart := splitRepoTag(req.Image)
	err := c.Call("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
	if err != nil {
		return "", fmt.Errorf("pull image: %w", err)
	}

	// create --rm container
	var containerResp dockertypes.ContainerCreateResponse
	err = c.Call("POST", "/containers/create", req, &containerResp)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	// start container
	err = c.Call("POST", "/containers/"+containerResp.ID+"/start", nil, nil)
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	return containerResp.ID, nil
}

func (c *Client) KillContainer(cid string) error {
	err := c.Call("POST", "/containers/"+cid+"/kill", nil, nil)
	if err != nil {
		return fmt.Errorf("kill container: %w", err)
	}
	return nil
}

func (c *Client) ListContainers(all bool) ([]*dockertypes.ContainerSummary, error) {
	url := "/containers/json"
	if all {
		url = "/containers/json?all=true"
	}

	var containers []*dockertypes.ContainerSummary
	err := c.Call("GET", url, nil, &containers)
	if err != nil {
		return nil, fmt.Errorf("get containers: %w", err)
	}

	return containers, nil
}

func (c *Client) InspectContainer(cid string) (*dockertypes.ContainerJSON, error) {
	var fullCtr dockertypes.ContainerJSON
	err := c.Call("GET", "/containers/"+cid+"/json", nil, &fullCtr)
	if err != nil {
		return nil, fmt.Errorf("get container: %w", err)
	}

	return &fullCtr, nil
}
