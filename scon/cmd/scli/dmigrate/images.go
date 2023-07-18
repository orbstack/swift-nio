package dmigrate

import (
	"fmt"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

func (m *Migrator) migrateOneImage(idx int, img dockertypes.Image, userName string) error {
	names := []string{img.ID}
	names = append(names, img.RepoTags...)

	// open export conn
	logrus.Infof("Migrating image %s", userName)
	err := scli.Client().InternalDockerMigrationLoadImage(types.InternalDockerMigrationLoadImageRequest{
		RemoteImageNames: names,
	})
	if err != nil {
		return fmt.Errorf("stream image: %w", err)
	}

	return nil
}

func (m *Migrator) submitImages(group *pond.TaskGroup, images []dockertypes.Image) error {
	for idx, img := range images {
		var userName string
		if len(img.RepoTags) > 0 {
			userName = img.RepoTags[0]
		} else {
			userName = img.ID
		}

		idx := idx
		img := img
		group.Submit(func() {
			err := m.migrateOneImage(idx, img, userName)
			if err != nil {
				panic(fmt.Errorf("image %s: %w", userName, err))
			}
		})
	}

	return nil
}
