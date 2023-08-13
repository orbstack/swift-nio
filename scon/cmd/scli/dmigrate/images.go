package dmigrate

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

var (
	errUnsupported = errors.New("unsupported")
)

type imageV2Manifest struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

func (m *Migrator) sendImageFastpath(img *dockertypes.ImageSummary) error {
	// [src] get full image info
	var fullImg dockertypes.FullImage
	err := m.srcClient.Call("GET", "/images/"+img.ID+"/json", nil, &fullImg)
	if err != nil {
		return fmt.Errorf("get full image info: %w", err)
	}

	// compat checks
	if fullImg.GraphDriver.Name != "overlay2" {
		return fmt.Errorf("unsupported graph driver: %s (%w)", fullImg.GraphDriver.Name, errUnsupported)
	}
	if !strings.HasPrefix(fullImg.ID, "sha256:") {
		return fmt.Errorf("unsupported image ID: %s (%w)", fullImg.ID, errUnsupported)
	}
	if fullImg.RootFS.Type != "layers" || len(fullImg.RootFS.Layers) == 0 {
		return fmt.Errorf("unsupported rootfs type: %s (%w)", fullImg.RootFS.Type, errUnsupported)
	}

	lowerDirStr := fullImg.GraphDriver.Data["LowerDir"]
	upperDir := fullImg.GraphDriver.Data["UpperDir"]
	if upperDir == "" {
		return fmt.Errorf("missing lower/upper dir (%w)", errUnsupported)
	}
	// no lower dirs is fine. it means there's only 1 layer
	lowerDirs := strings.Split(lowerDirStr, ":")
	if lowerDirStr == "" {
		lowerDirs = nil
	}
	// prepend upper dir
	overlayDirs := append([]string{upperDir}, lowerDirs...)
	// sanity check: validate dirs
	for _, dir := range overlayDirs {
		if !strings.HasPrefix(dir, "/var/lib/docker/overlay2/") || !strings.HasSuffix(dir, "/diff") {
			return fmt.Errorf("invalid lower dir: %s (%w)", dir, errUnsupported)
		}
	}

	// [src] extract layer info and construct commands
	// manifest Layers order: top = last
	// overlay order: Upper = top, then top = first, ... in LowerDir. (reversed)
	// so first, iterate through LowerDir in reverse order
	// these are technically wrong
	tmpDir := "/tmp/" + img.ID
	var cmds []string
	var layerTars []string
	for i := len(overlayDirs) - 1; i >= 0; i-- {
		overlayDir := overlayDirs[i]
		layerTar := fmt.Sprintf("layer%d.tar", i)
		diffIdIdx := len(layerTars)
		layerDiffId := fullImg.RootFS.Layers[diffIdIdx]
		cmds = append(cmds, fmt.Sprintf("GODEBUG=asyncpreemptoff=1 ocitar %s/%s %s %s", tmpDir, layerTar, layerDiffId, shellescape.Quote(overlayDir)))
		layerTars = append(layerTars, layerTar)
	}

	// create manifest
	manifest := []imageV2Manifest{
		{
			Config:   "config.json",
			RepoTags: fullImg.RepoTags,
			Layers:   layerTars,
		},
	}
	// json
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	rawImageID := strings.TrimPrefix(img.ID, "sha256:")

	dirSyncReq := types.InternalDockerMigrationSyncDirsRequest{
		JobID: rand.Uint64(),
		Dirs:  []string{types.DockerMigrationSyncDirImageLoad},
	}
	dirSyncReqBytes, err := json.Marshal(&dirSyncReq)
	if err != nil {
		return fmt.Errorf("marshal dir sync req: %w", err)
	}

	cmdBuilder := func(port int) []string {
		imgScript := fmt.Sprintf(`
			set -eo pipefail
			mkdir -p %s
			trap "rm -rf %s" EXIT
			cd %s
			%s
			cp /var/lib/docker/image/overlay2/imagedb/content/sha256/%s config.json
			echo %s | base64 -d > manifest.json
			(echo %s; tar -cf - .) > /dev/tcp/host.docker.internal/%d
		`, tmpDir, tmpDir, tmpDir, strings.Join(cmds, "\n"), rawImageID, shellescape.Quote(base64.StdEncoding.EncodeToString(manifestBytes)), shellescape.Quote(string(dirSyncReqBytes)), port)
		return []string{"bash", "-c", imgScript}
	}
	return m.syncDirsGeneric(m.srcClient, cmdBuilder, "/", m.destClient, &dirSyncReq)
}

func (m *Migrator) migrateOneImage(idx int, img *dockertypes.ImageSummary, userName string) error {
	logrus.Infof("Migrating image %s", userName)

	// try fastpath
	err := m.sendImageFastpath(img)
	if err == nil {
		return nil
	}
	logrus.Warnf("fastpath failed: %s", err)

	// open export conn
	names := []string{img.ID}
	names = append(names, img.RepoTags...)
	err = scli.Client().InternalDockerMigrationLoadImage(types.InternalDockerMigrationLoadImageRequest{
		RemoteImageNames: names,
	})
	if err != nil {
		return fmt.Errorf("load image: %w", err)
	}

	return nil
}

func (m *Migrator) submitImages(group *pond.TaskGroup, images []*dockertypes.ImageSummary) error {
	for idx, img := range images {
		var userName string
		if len(img.RepoTags) > 0 {
			userName = img.RepoTags[0]
		} else {
			userName = img.ID
		}

		idx := idx
		img := img
		logrus.WithField("image", userName).Debug("submitting image")
		group.Submit(func() {
			defer m.finishOneEntity(&entitySpec{imageID: img.ID})

			err := m.migrateOneImage(idx, img, userName)
			if err != nil {
				panic(fmt.Errorf("image %s: %w", userName, err))
			}
		})
	}

	return nil
}
