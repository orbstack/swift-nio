package dockerclient

import (
	"fmt"
	"net"
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
		fmt.Println("Pulling image")
		err := c.Call("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
		if err != nil {
			return "", fmt.Errorf("pull image: %w", err)
		}
		fmt.Println("Pulled image")
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
	err = c.Call("POST", "/containers/"+url.PathEscape(containerResp.ID)+"/start", nil, nil)
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	return containerResp.ID, nil
}

func (c *Client) InteractiveRunContainer(req *dockertypes.ContainerCreateRequest, pullImage bool) (net.Conn, string, error) {
	if pullImage {
		// need to pull image first
		repoPart, tagPart := splitRepoTag(req.Image)
		err := c.Call("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
		if err != nil {
			return nil, "", fmt.Errorf("pull image: %w", err)
		}
	}

	// create --rm container
	var containerResp dockertypes.ContainerCreateResponse
	err := c.Call("POST", "/containers/create", req, &containerResp)
	if err != nil {
		return nil, "", fmt.Errorf("create container: %w", err)
	}

	// upgrade to tcp
	conn, err := c.StreamHijack("POST", "/containers/"+containerResp.ID+"/attach?logs=true&stream=true&stdin=true&stdout=true&stderr=true", nil)
	if err != nil {
		return nil, "", fmt.Errorf("attach container: %w", err)
	}

	// start container
	err = c.Call("POST", "/containers/"+containerResp.ID+"/start", nil, nil)
	if err != nil {
		return nil, "", fmt.Errorf("start container: %w", err)
	}
	return conn, containerResp.ID, nil
}

func (c *Client) KillContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/kill", nil, nil)
	if err != nil {
		return fmt.Errorf("kill container: %w", err)
	}
	return nil
}

func (c *Client) StopContainer(cid string) error {
	err := c.Call("POST", "/containers/"+cid+"/stop", nil, nil)
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
	err := c.Call("GET", "/containers/"+url.PathEscape(cid)+"/json", nil, &fullCtr)
	if err != nil {
		return nil, fmt.Errorf("get container: %w", err)
	}

	return &fullCtr, nil
}

func (c *Client) CommitContainer(containerID string) (string, error) {
	var resp dockertypes.ContainerCommitResponse
	err := c.Call("POST", "/commit?container="+url.QueryEscape(containerID), nil, &resp)
	if err != nil {
		return "", fmt.Errorf("commit container: %w", err)
	}

	return resp.ID, nil
}

func (c *Client) RemoveContainer(cid string, force bool) error {
	err := c.Call("DELETE", "/containers/"+url.PathEscape(cid)+"?force="+strconv.FormatBool(force), nil, nil)
	if err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

func (c *Client) DiffContainer(cid string) ([]dockertypes.ContainerDiffEntry, error) {
	var diffs []dockertypes.ContainerDiffEntry
	err := c.Call("GET", "/containers/"+url.PathEscape(cid)+"/changes", nil, &diffs)
	if err != nil {
		return nil, fmt.Errorf("diff container: %w", err)
	}

	return diffs, nil
}

func (c *Client) WaitContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/wait", nil, nil)
	if err != nil {
		return fmt.Errorf("wait container: %w", err)
	}
	return nil
}
