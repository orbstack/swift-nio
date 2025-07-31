package dockerclient

import (
	"fmt"
	"io"
	"net/url"
	"strconv"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

func (c *Client) ListImages() ([]*dockertypes.ImageSummary, error) {
	var images []*dockertypes.ImageSummary
	err := c.Call("GET", "/images/json?shared-size=1", nil, &images)
	if err != nil {
		return nil, fmt.Errorf("get images: %w", err)
	}

	return images, nil
}

func (c *Client) InspectImage(imageID string) (*dockertypes.FullImageWithConfig, error) {
	var full *dockertypes.FullImageWithConfig
	err := c.Call("GET", "/images/"+url.PathEscape(imageID)+"/json", nil, &full)
	if err != nil {
		return nil, fmt.Errorf("inspect image %s: %w", imageID, err)
	}
	return full, nil
}

func (c *Client) ListImagesFull() ([]dockertypes.Image, error) {
	imageSummaries, err := c.ListImages()
	if err != nil {
		return nil, err
	}

	res := make([]dockertypes.Image, 0, len(imageSummaries))

	for _, summary := range imageSummaries {
		full, err := c.InspectImage(summary.ID)
		if err != nil {
			if IsStatusError(err, 404) {
				continue
			} else {
				return nil, fmt.Errorf("get image %s: %w", summary.ID, err)
			}
		}

		// not returning a ptr b/c it's just the size of two ptrs
		res = append(res, dockertypes.Image{Summary: summary, Full: full})
	}

	return res, nil
}

func (c *Client) DeleteImage(imageID string, force bool) error {
	err := c.Call("DELETE", "/images/"+url.PathEscape(imageID)+"?force="+strconv.FormatBool(force), nil, nil)
	if err != nil {
		return fmt.Errorf("remove image %s: %w", imageID, err)
	}
	return nil
}

func (c *Client) ImportImage(reader io.Reader) error {
	err := c.Call("POST", "/images/load?quiet=true", reader, nil)
	if err != nil {
		return fmt.Errorf("import image: %w", err)
	}
	return nil
}

func (c *Client) ExportImage(imageID string) (io.ReadCloser, error) {
	resp, err := c.CallRaw("GET", "/images/"+url.PathEscape(imageID)+"/get", nil)
	if err != nil {
		return nil, fmt.Errorf("export image %s: %w", imageID, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ReadError(resp)
	}

	return resp.Body, nil
}
