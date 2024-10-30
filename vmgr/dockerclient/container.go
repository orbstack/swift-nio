package dockerclient

import (
	"fmt"
	"net/url"
	"strconv"
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

type RunContainerOptions struct {
	Name      string
	PullImage bool
}

func (c *Client) RunContainer(opts RunContainerOptions, req *dockertypes.ContainerCreateRequest) (string, error) {
	if opts.PullImage {
		// need to pull image first
		repoPart, tagPart := splitRepoTag(req.Image)
		err := c.Call("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
		if err != nil {
			return "", fmt.Errorf("pull image: %w", err)
		}
	}

	path := "/containers/create"
	if opts.Name != "" {
		path += "?name=" + url.QueryEscape(opts.Name)
	}

	// create --rm container
	var containerResp dockertypes.ContainerCreateResponse
	err := c.Call("POST", path, req, &containerResp)
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

func (c *Client) CommitContainer(containerID string) (string, error) {
	var resp dockertypes.ContainerCommitResponse
	err := c.Call("POST", "/commit?container="+containerID, nil, &resp)
	if err != nil {
		return "", fmt.Errorf("commit container: %w", err)
	}

	return resp.ID, nil
}

func (c *Client) RemoveContainer(cid string, force bool) error {
	err := c.Call("DELETE", "/containers/"+cid+"?force="+strconv.FormatBool(force), nil, nil)
	if err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}
