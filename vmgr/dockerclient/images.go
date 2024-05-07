package dockerclient

import (
	"encoding/json"
	"fmt"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

type Image struct {
	Summary *dockertypes.ImageSummary
	Full    *dockertypes.FullImage
}

func (c *Client) ListImages() ([]*dockertypes.ImageSummary, error) {
	var images []*dockertypes.ImageSummary
	err := c.Call("GET", "/images/json?shared-size=1", nil, &images)
	if err != nil {
		return nil, fmt.Errorf("get images: %w", err)
	}

	return images, nil
}

func (c *Client) ListImagesFull() ([]Image, error) {
	imageSummaries, err := c.ListImages()
	if err != nil {
		return nil, err
	}

	res := make([]Image, 0, len(imageSummaries))

	for _, summary := range imageSummaries {
		resp, err := c.CallRaw("GET", "/images/"+summary.ID+"/json", nil)
		if err != nil {
			return nil, fmt.Errorf("get image %s: %w", summary.ID, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == 404 {
			continue
		} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("get image %s: %w", summary.ID, ReadError(resp))
		}

		var full *dockertypes.FullImage

		err = json.NewDecoder(resp.Body).Decode(&full)
		if err != nil {
			return nil, fmt.Errorf("parse image %s: %w", summary.ID, err)
		}

		// not returning a ptr b/c it's just the size of two ptrs
		res = append(res, Image{Summary: summary, Full: full})
	}

	return res, nil
}
