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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const (
	RepoLxd    = "https://images.linuxcontainers.org"
	RepoUbuntu = "https://cloud-images.ubuntu.com/releases"

	maxSquashfsCpus      = 4
	imageDownloadTimeout = 15 * time.Minute
)

var (
	extraImages = map[types.ImageSpec]RawImage{
		// too much work to keep sha256 and size up to date
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
	resp, err := imagesHttpClient.Get(RepoLxd + "/streams/v1/images.json")
	if err != nil {
		return nil, fmt.Errorf("get index: %w", err)
	}
	defer resp.Body.Close()

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
		sort.Strings(versions)
		version := versions[len(versions)-1]

		items := product.Versions[version].Items
		img := RawImage{
			Revision: version,
		}

		// lxd.tar.xz
		if item, ok := findItem(items, "lxd.tar.xz"); ok {
			img.MetadataURL = RepoLxd + "/" + item.Path
			img.MetadataSha256 = item.Sha256
		}

		// take squashfs if available
		if item, ok := findItem(items, "squashfs"); ok {
			img.RootfsFormat = ImageFormatSquashfs
			img.RootfsURL = RepoLxd + "/" + item.Path
			img.RootfsSha256 = item.Sha256
			img.Size = item.Size
		} else if item, ok := findItem(items, "root.tar.xz"); ok {
			// otherwise, take tar.xz
			img.RootfsFormat = ImageFormatTarXz
			img.RootfsURL = RepoLxd + "/" + item.Path
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

func isDistroInLxdRepo(distro string) bool {
	switch distro {
	case images.ImageAlpine, images.ImageArch, images.ImageCentos, images.ImageDebian, images.ImageFedora, images.ImageGentoo, images.ImageKali, images.ImageOpensuse, images.ImageUbuntu, images.ImageVoid, images.ImageDevuan, images.ImageAlma, images.ImageOracle, images.ImageRocky:
		return true
	}

	return false
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
	if resp.StatusCode != http.StatusOK {
		return errors.New("bad status code: " + resp.Status)
	}

	// check sha256
	hash := sha256.New()
	tee := io.TeeReader(resp.Body, hash)

	// write to file
	_, err = io.Copy(out, tee)
	if err != nil {
		return fmt.Errorf("stream download to file: %w", err)
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

func (m *ConManager) makeRootfsWithImage(spec types.ImageSpec, containerName string, rootfsDir string) error {
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
		case isDistroInLxdRepo(spec.Distro):
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
		cmd = exec.Command("tar", "-xJf", rootfsFile, "-C", rootfsDir, "--numeric-owner", "--xattrs-include=*")
	case ImageFormatSquashfs:
		// limit parallelism
		procs := runtime.NumCPU()
		if procs > maxSquashfsCpus {
			procs = maxSquashfsCpus
		}
		cmd = exec.Command("unsquashfs", "-n", "-f", "-p", strconv.Itoa(procs), "-d", rootfsDir, rootfsFile)
	default:
		return fmt.Errorf("unsupported rootfs format: %v", img.RootfsFormat)
	}
	output, err := cmd.CombinedOutput()
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
	output, err = cmd.CombinedOutput()
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
		return fmt.Errorf("unmarshal metadata: %w", err)
	}
	logrus.WithField("metadata", meta).Debug("loaded metadata")

	// apply templates
	logrus.Info("applying templates")
	for relPath, templateSpec := range meta.Templates {
		logrus.WithField("path", relPath).Debug("applying template")
		if strings.ContainsRune(templateSpec.Template, '/') {
			return errors.New("template path must not contain '/': " + templateSpec.Template)
		}

		tmplBytes, err := os.ReadFile(path.Join(metadataDir, "templates", templateSpec.Template))
		if err != nil {
			return err
		}
		tmpl := string(tmplBytes)

		// terrible...
		// TODO proper templating
		tmpl = strings.ReplaceAll(tmpl, "{{ container.name }}", containerName)

		writePath, err := securejoin.SecureJoin(rootfsDir, strings.TrimPrefix(relPath, "/"))
		if err != nil {
			return err
		}
		// make dirs
		err = os.MkdirAll(path.Dir(writePath), 0755)
		if err != nil {
			return err
		}
		err = os.WriteFile(writePath, []byte(tmpl), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}
