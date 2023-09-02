package agent

import (
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

type cachedImage struct {
	TagRefs int
	Image   *dockertypes.FullImage
}

func (d *DockerAgent) getFullImage(id string) (*dockertypes.FullImage, error) {
	// use cache if possible
	if fullImage, ok := d.fullImageCache[id]; ok {
		return fullImage.Image, nil
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
	// TODO this shouldn't be a pointer but must be due to generics. and it'll probably become one due to Diff[T] generics anyway
	newImages := make([]*sgtypes.TaggedImage, 0, len(newImageSummaries))
	for _, s := range newImageSummaries {
		// skip untagged images
		// so when an image gets its first tag, it'll be added
		if len(s.RepoTags) == 0 {
			continue
		}

		// same for all tags - no need to refetch
		fullImage, err := d.getFullImage(s.ID)
		if err != nil {
			return err
		}

		for _, tag := range s.RepoTags {
			newImages = append(newImages, &sgtypes.TaggedImage{
				Tag:   tag,
				Image: fullImage,
			})
		}
	}

	// diff
	removed, added := util.DiffSlicesKey(d.lastImages, newImages)

	// tell scon
	err = d.scon.OnDockerImagesChanged(sgtypes.Diff[*sgtypes.TaggedImage]{
		Added:   added,
		Removed: removed,
	})
	if err != nil {
		logrus.WithError(err).Error("failed to update scon images")
	}

	d.lastImages = newImages
	// update full img cache
	// must remove before add in case of image rebuild with same tag
	for _, timg := range removed {
		if cached, ok := d.fullImageCache[timg.Image.ID]; ok {
			cached.TagRefs--
			// reassign since it's a value type
			d.fullImageCache[timg.Image.ID] = cached
			if cached.TagRefs == 0 {
				delete(d.fullImageCache, timg.Image.ID)
			}
		}
	}
	for _, timg := range added {
		if cached, ok := d.fullImageCache[timg.Image.ID]; ok {
			cached.TagRefs++
			// reassign since it's a value type
			d.fullImageCache[timg.Image.ID] = cached
		} else {
			d.fullImageCache[timg.Image.ID] = cachedImage{
				TagRefs: 1,
				Image:   timg.Image,
			}
		}
	}
	return nil
}
