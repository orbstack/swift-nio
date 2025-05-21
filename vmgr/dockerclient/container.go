package dockerclient

import (
	"fmt"
	"io"
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

type PullImageMode int

const (
	PullImageNever PullImageMode = iota
	PullImageIfMissing
	PullImageAlways
)

type PullImageOptions struct {
	ProgressOut         io.Writer
	IsTerminal          bool
	TerminalFd          uintptr
	PullingFromOverride *string
}

type RunContainerOptions struct {
	Name          string
	PullImage     PullImageMode
	PullImageOpts *PullImageOptions
}

func pullImage(c *Client, image string, opts *PullImageOptions) error {
	repoPart, tagPart := splitRepoTag(image)
	if opts == nil || opts.ProgressOut == nil {
		return c.Call("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
	} else {
		return c.CallStream("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, opts.ProgressOut, opts.IsTerminal, opts.TerminalFd, opts.PullingFromOverride)
	}
}

func (c *Client) RunContainer(opts RunContainerOptions, req *dockertypes.ContainerCreateRequest) (string, error) {
	var err error
	if opts.PullImage == PullImageAlways {
		err = pullImage(c, req.Image, opts.PullImageOpts)
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
	err = c.Call("POST", path, req, &containerResp)
	if IsStatusError(err, 404) && opts.PullImage == PullImageIfMissing {
		// pull and retry if user requested pull image if missing
		err = pullImage(c, req.Image, opts.PullImageOpts)
		if err != nil {
			return "", fmt.Errorf("pull image: %w", err)
		}
		err = c.Call("POST", path, req, &containerResp)
	}
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

func (c *Client) StartContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/start", nil, nil)
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

func (c *Client) KillContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/kill", nil, nil)
	if err != nil {
		return fmt.Errorf("kill container: %w", err)
	}
	return nil
}

func (c *Client) StopContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/stop", nil, nil)
	if err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

func (c *Client) RestartContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/restart", nil, nil)
	if err != nil {
		return fmt.Errorf("restart container: %w", err)
	}
	return nil
}

func (c *Client) PauseContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/pause", nil, nil)
	if err != nil {
		return fmt.Errorf("pause container: %w", err)
	}
	return nil
}

func (c *Client) UnpauseContainer(cid string) error {
	err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/unpause", nil, nil)
	if err != nil {
		return fmt.Errorf("unpause container: %w", err)
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

func (c *Client) DeleteContainer(cid string, force bool) error {
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
