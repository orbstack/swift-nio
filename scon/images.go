package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const (
	RepoOrb    = "https://cdn-images.orbstack.dev"
	RepoUbuntu = "https://cloud-images.ubuntu.com/releases"

	maxSquashfsCpus      = 4
	imageDownloadTimeout = 15 * time.Minute
)

// fix growpart and resizefs errors on boot when using cloud-init
const cloudInitVendorData = `#cloud-config
resize_rootfs: false
growpart:
  mode: 'off'
`

var (
	extraImages = map[types.ImageSpec]RawImage{
		// too much work to keep sha256 and size up to date
		// [BREAKING] for legacy compat reasons we keep urls for these version combos,
		// but now we use lxc-ci images because it's much faster than nixos hydra server
		{Distro: images.ImageNixos, Version: "22.11", Arch: "amd64", Variant: "default"}: {
			MetadataURL:    "https://hydra.nixos.org/job/nixos/release-22.11/nixos.lxdMeta.x86_64-linux/latest/download-by-type/file/system-tarball",
			MetadataSha256: "",
			RootfsFormat:   ImageFormatTarXz,
			RootfsURL:      "https://hydra.nixos.org/job/nixos/release-22.11/nixos.lxdImage.x86_64-linux/latest/download-by-type/file/system-tarball",
			RootfsSha256:   "",
			Size:           0,
			Revision:       "hydra-latest",
		},
		{Distro: images.ImageNixos, Version: "22.11", Arch: "arm64", Variant: "default"}: {
			MetadataURL:    "https://hydra.nixos.org/job/nixos/release-22.11/nixos.lxdMeta.aarch64-linux/latest/download-by-type/file/system-tarball",
			MetadataSha256: "",
			RootfsFormat:   ImageFormatTarXz,
			RootfsURL:      "https://hydra.nixos.org/job/nixos/release-22.11/nixos.lxdImage.aarch64-linux/latest/download-by-type/file/system-tarball",
			RootfsSha256:   "",
			Size:           0,
			Revision:       "hydra-latest",
		},
		{Distro: images.ImageNixos, Version: "23.05", Arch: "amd64", Variant: "default"}: {
			MetadataURL:    "https://hydra.nixos.org/job/nixos/release-23.05/nixos.lxdMeta.x86_64-linux/latest/download-by-type/file/system-tarball",
			MetadataSha256: "",
			RootfsFormat:   ImageFormatTarXz,
			RootfsURL:      "https://hydra.nixos.org/job/nixos/release-23.05/nixos.lxdImage.x86_64-linux/latest/download-by-type/file/system-tarball",
			RootfsSha256:   "",
			Size:           0,
			Revision:       "hydra-latest",
		},
		{Distro: images.ImageNixos, Version: "23.05", Arch: "arm64", Variant: "default"}: {
			MetadataURL:    "https://hydra.nixos.org/job/nixos/release-23.05/nixos.lxdMeta.aarch64-linux/latest/download-by-type/file/system-tarball",
			MetadataSha256: "",
			RootfsFormat:   ImageFormatTarXz,
			RootfsURL:      "https://hydra.nixos.org/job/nixos/release-23.05/nixos.lxdImage.aarch64-linux/latest/download-by-type/file/system-tarball",
			RootfsSha256:   "",
			Size:           0,
			Revision:       "hydra-latest",
		},
	}
)

type ImageFormat int

const (
	ImageFormatTarXz ImageFormat = iota
	ImageFormatSquashfs
)

type StreamsImage struct {
	Ftype  string `json:"ftype"`
	Sha256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Path   string `json:"path"`
}

type StreamsImages struct {
	Products map[string]struct {
		Versions map[string]struct {
			Items map[string]StreamsImage `json:"items"`
		} `json:"versions"`
	} `json:"products"`
}

type RawImage struct {
	MetadataURL    string
	MetadataSha256 string
	RootfsFormat   ImageFormat
	RootfsURL      string
	RootfsSha256   string
	Size           int64
	Revision       string
}

type ImageMetadata struct {
	Templates map[string]ImageTemplate `yaml:"templates"`
}

type ImageTemplate struct {
	When       []string          `yaml:"when"`
	CreateOnly bool              `yaml:"create_only"`
	Properties map[string]string `yaml:"properties"`
	Template   string            `yaml:"template"`
}

var imagesHttpClient = &http.Client{
	// includes download time
	Timeout: 30 * time.Minute,
	Transport: &http.Transport{
		MaxIdleConns:    2,
		IdleConnTimeout: 1 * time.Minute,
		// nixos hydra can be slow
		ResponseHeaderTimeout: 1 * time.Minute,
		TLSHandshakeTimeout:   30 * time.Second,
		ExpectContinueTimeout: 30 * time.Second,
		ReadBufferSize:        32768,
	},
}

func findItem(items map[string]StreamsImage, ftype string) (*StreamsImage, bool) {
	for _, item := range items {
		if item.Ftype == ftype {
			return &item, true
		}
	}

	return nil, false
}

func fetchStreamsImages() (map[types.ImageSpec]RawImage, error) {
	resp, err := imagesHttpClient.Get(RepoOrb + "/streams/v1/images.json")
	if err != nil {
		return nil, fmt.Errorf("get index: %w", err)
	}
	defer resp.Body.Close()

	// check status code
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("GET index status: " + resp.Status)
	}

	var images StreamsImages
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}

	imagesMap := make(map[types.ImageSpec]RawImage)
	for imageKey, product := range images.Products {
		// sort and pick latest version
		var versions []string
		for version := range product.Versions {
			versions = append(versions, version)
		}
		slices.Sort(versions)
		version := versions[len(versions)-1]

		items := product.Versions[version].Items
		img := RawImage{
			Revision: version,
		}

		// prefer orb.tar.xz, then incus.tar.xz
		if item, ok := findItem(items, "orb.tar.xz"); ok {
			img.MetadataURL = RepoOrb + "/" + item.Path
			img.MetadataSha256 = item.Sha256
		} else if item, ok := findItem(items, "incus.tar.xz"); ok {
			img.MetadataURL = RepoOrb + "/" + item.Path
			img.MetadataSha256 = item.Sha256
		}

		// take squashfs if available
		if item, ok := findItem(items, "squashfs"); ok {
			img.RootfsFormat = ImageFormatSquashfs
			img.RootfsURL = RepoOrb + "/" + item.Path
			img.RootfsSha256 = item.Sha256
			img.Size = item.Size
		} else if item, ok := findItem(items, "root.tar.xz"); ok {
			// otherwise, take tar.xz
			img.RootfsFormat = ImageFormatTarXz
			img.RootfsURL = RepoOrb + "/" + item.Path
			img.RootfsSha256 = item.Sha256
			img.Size = item.Size
		}

		// make sure we got everything
		if img.RootfsURL == "" {
			// ubuntu:jammy:amd64:desktop only has VM disk image
			continue
		}
		if img.MetadataURL == "" || img.RootfsURL == "" || img.Revision == "" {
			return nil, errors.New("missing metadata or rootfs for " + imageKey)
		}

		// split the key
		parts := strings.Split(imageKey, ":")
		if len(parts) != 4 {
			return nil, errors.New("invalid image key: " + imageKey)
		}
		spec := types.ImageSpec{
			Distro:  parts[0],
			Version: parts[1],
			Arch:    parts[2],
			Variant: parts[3],
		}
		imagesMap[spec] = img
	}

	return imagesMap, nil
}

func isImageDistroInLxdRepo(image string) bool {
	return image != images.ImageDocker
}

func downloadFile(url string, outPath string, expectSha256 string) error {
	// create temp file
	tmpPath := outPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	defer out.Close()

	// download
	ctx, cancel := context.WithTimeout(context.Background(), imageDownloadTimeout)
	defer cancel()

	logrus.WithField("url", url).Debug("downloading file")
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	// workaround for unexpected EOF?
	req.Close = true
	req = req.WithContext(ctx)
	resp, err := imagesHttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("start GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	// check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("GET download status: " + resp.Status)
	}

	// check sha256
	hash := sha256.New()
	tee := io.TeeReader(resp.Body, hash)

	// write to file
	_, err = io.Copy(out, tee)
	if err != nil {
		return fmt.Errorf("stream to file: %w", err)
	}

	// check sha256 if we have one
	if expectSha256 != "" && hex.EncodeToString(hash.Sum(nil)) != expectSha256 {
		return errors.New("sha256 mismatch for " + url)
	}

	// rename
	err = os.Rename(tmpPath, outPath)
	if err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

func (m *ConManager) makeRootfsWithImage(spec types.ImageSpec, containerName string, rootfsDir string, cloudInitUserData string) error {
	// create temp in subdir
	downloadDir, err := os.MkdirTemp(m.subdir("images"), "download")
	if err != nil {
		return err
	}
	defer os.RemoveAll(downloadDir)

	// fetch index
	var img RawImage
	var ok bool
	img, ok = extraImages[spec]
	if !ok {
		switch {
		case isImageDistroInLxdRepo(spec.Distro):
			logrus.Info("fetching image index")
			images, err := fetchStreamsImages()
			if err != nil {
				return fmt.Errorf("fetch index: %w", err)
			}
			img, ok = images[spec]
		default:
			return errors.New("unsupported distro: " + spec.Distro)
		}
	}
	if !ok {
		return fmt.Errorf("image not found: %v", spec)
	}

	// download metadata and rootfs in parallel
	logrus.WithField("spec", spec).Info("downloading images")
	var wg sync.WaitGroup
	wg.Add(2)
	var metadataErr, rootfsErr error
	rootfsFile := downloadDir + "/rootfs"
	metaFile := downloadDir + "/meta"
	go func() {
		defer wg.Done()
		metadataErr = downloadFile(img.MetadataURL, metaFile, img.MetadataSha256)
	}()
	go func() {
		defer wg.Done()
		rootfsErr = downloadFile(img.RootfsURL, rootfsFile, img.RootfsSha256)
	}()
	wg.Wait()

	// check errors
	if metadataErr != nil {
		return fmt.Errorf("download metadata: %w", metadataErr)
	}
	if rootfsErr != nil {
		return fmt.Errorf("download rootfs: %w", rootfsErr)
	}

	// extract rootfs
	logrus.WithField("container", containerName).Info("extracting rootfs")
	var cmd *exec.Cmd
	switch img.RootfsFormat {
	case ImageFormatTarXz:
		cmd = exec.Command("tar", "-xJf", rootfsFile, "-C", rootfsDir, "--numeric-owner", "--xattrs", "--xattrs-include=*")
	case ImageFormatSquashfs:
		// limit parallelism
		procs := min(runtime.NumCPU(), maxSquashfsCpus)
		cmd = exec.Command("unsquashfs", "-n", "-f", "-p", strconv.Itoa(procs), "-d", rootfsDir, rootfsFile)
	default:
		return fmt.Errorf("unsupported rootfs format: %v", img.RootfsFormat)
	}
	output, err := util.WithDefaultOom2(func() ([]byte, error) {
		return cmd.CombinedOutput()
	})
	if err != nil {
		return fmt.Errorf("extract rootfs: %w\n%s", err, output)
	}

	// make temp dir for metadata
	metadataDir, err := os.MkdirTemp(m.tmpDir, "metadata")
	if err != nil {
		return err
	}
	defer os.RemoveAll(metadataDir)

	// extract metadata
	cmd = exec.Command("tar", "-xf", metaFile, "-C", metadataDir)
	output, err = util.WithDefaultOom2(func() ([]byte, error) {
		return cmd.CombinedOutput()
	})
	if err != nil {
		return fmt.Errorf("extract metadata: %w\n%s", err, output)
	}

	// load metadata
	metadataPath := metadataDir + "/metadata.yaml"
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}
	var meta ImageMetadata
	err = yaml.Unmarshal(metadataBytes, &meta)
	if err != nil {
		return fmt.Errorf("parse metadata: %w", err)
	}
	logrus.WithField("metadata", meta).Debug("loaded metadata")

	fs, err := securefs.NewFromPath(rootfsDir)
	if err != nil {
		return err
	}
	defer fs.Close()

	// NixOS special case
	hostName := containerName
	if spec.Distro == images.ImageNixos {
		hostName = strings.ReplaceAll(hostName, ".", "-")
	}

	// apply templates
	logrus.Info("applying templates")
	ctx := pongo2.Context{
		"container": map[string]string{
			"name": hostName,
		},
		"instance": map[string]string{
			"name": hostName,
			"type": "container", // not virtual-machine. for cloud-init network-config
		},
		"properties": map[string]string{
			// used by cloud-init templates as default value for config_get
			"default": "",
		},
		// cloud-init.user-data is newer. new images still have compat for user.user-data
		// so use user.user-data for better compat with old images
		"config_get": func(key string, def any) any {
			if key == "user.user-data" {
				return cloudInitUserData
			} else if key == "user.vendor-data" {
				return cloudInitVendorData
			}

			return def
		},
	}
	for relPath, templateSpec := range meta.Templates {
		logrus.WithField("path", relPath).Debug("applying template")
		if strings.ContainsRune(templateSpec.Template, '/') {
			return errors.New("template path must not contain '/': " + templateSpec.Template)
		}

		tplBytes, err := os.ReadFile(path.Join(metadataDir, "templates", templateSpec.Template))
		if err != nil {
			return err
		}

		tpl, err := pongo2.FromString("{% autoescape off %}" + string(tplBytes) + "{% endautoescape %}")
		if err != nil {
			return fmt.Errorf("parse template: %w", err)
		}

		result, err := tpl.Execute(ctx)
		if err != nil {
			return fmt.Errorf("execute template: %w", err)
		}

		// make dirs
		err = fs.MkdirAll(path.Dir(relPath), 0755)
		if err != nil {
			return err
		}
		err = fs.WriteFile(relPath, []byte(result), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}
