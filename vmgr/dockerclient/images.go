package dockerclient

import (
	"fmt"
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
		res = append(res, dockertypes.Image{Summary: summary, Full: &full.FullImage})
	}

	return res, nil
}

func (c *Client) RemoveImage(imageID string, force bool) error {
	err := c.Call("DELETE", "/images/"+url.PathEscape(imageID)+"?force="+strconv.FormatBool(force), nil, nil)
	if err != nil {
		return fmt.Errorf("remove image %s: %w", imageID, err)
	}
	return nil
}
