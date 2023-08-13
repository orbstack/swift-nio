package agent

import (
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

func (d *DockerAgent) getFullImage(id string) (*dockertypes.FullImage, error) {
	// use cache if possible
	if fullImage, ok := d.fullImageCache[id]; ok {
		return fullImage, nil
	}

	var fullImage dockertypes.FullImage
	err := d.client.Call("GET", "/images/"+id+"/json", nil, &fullImage)
	if err != nil {
		return nil, err
	}

	return &fullImage, nil
}

func (d *DockerAgent) refreshImages() error {
	var newImageSummaries []dockertypes.ImageSummary
	err := d.client.Call("GET", "/images/json", nil, &newImageSummaries)
	if err != nil {
		return err
	}

	// convert to full images
	// TODO: containerd image stoer can have images with same ID, diff arch
	newImages := make([]*dockertypes.FullImage, 0, len(newImageSummaries))
	for _, s := range newImageSummaries {
		// skip untagged images
		if len(s.RepoTags) == 0 {
			continue
		}

		fullImage, err := d.getFullImage(s.ID)
		if err != nil {
			return err
		}

		newImages = append(newImages, fullImage)
	}

	// diff
	added, removed := util.DiffSlicesKey[string](d.lastImages, newImages)

	// tell scon
	err = d.scon.OnDockerImagesChanged(sgtypes.Diff[*dockertypes.FullImage]{
		Added:   added,
		Removed: removed,
	})
	if err != nil {
		logrus.WithError(err).Error("failed to update scon images")
	}

	d.lastImages = newImages
	// update full img cache
	for _, img := range added {
		d.fullImageCache[img.ID] = img
	}
	for _, img := range removed {
		delete(d.fullImageCache, img.ID)
	}
	return nil
}
