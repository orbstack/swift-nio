package main

import (
	"errors"
	"os"
	"path"
	"runtime"
	"strconv"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/lxc/go-lxc"
	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
)

const (
	DistroAlpine   = "alpine"
	DistroArch     = "arch"
	DistroCentos   = "centos"
	DistroDebian   = "debian"
	DistroFedora   = "fedora"
	DistroGentoo   = "gentoo"
	DistroKali     = "kali"
	DistroOpensuse = "opensuse"
	DistroUbuntu   = "ubuntu"
	DistroVoid     = "void"
	DistroNixos    = "nixos"

	DistroDevuan  = "devuan"
	DistroAlma    = "alma"
	DistroAmazon  = "amazon"
	DistroApertis = "apertis"
	DistroOracle  = "oracle"
	DistroRocky   = "rocky"

	ImageAlpine   = "alpine"
	ImageArch     = "archlinux"
	ImageCentos   = "centos"
	ImageDebian   = "debian"
	ImageFedora   = "fedora"
	ImageGentoo   = "gentoo"
	ImageKali     = "kali"
	ImageOpensuse = "opensuse"
	ImageUbuntu   = "ubuntu"
	ImageVoid     = "voidlinux"
	ImageNixos    = "nixos"

	ImageDevuan  = "devuan"
	ImageAlma    = "almalinux"
	ImageAmazon  = "amazonlinux"
	ImageApertis = "apertis"
	ImageOracle  = "oracle"
	ImageRocky   = "rockylinux"
)

type ImageSpec struct {
	Distro  string
	Version string
	Arch    string
	Variant string
}

type ContainerRecord struct {
	ID    string
	Name  string
	Image ImageSpec

	Running  bool
	Deleting bool
}

type CreateParams struct {
	Name  string
	User  string
	Image ImageSpec

	UserPassword string
}

func getDefaultLxcArch() (string, error) {
	switch runtime.GOARCH {
	case "i386":
		return "i686", nil
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "armhf", nil
	default:
		return "", errors.New("unsupported architecture")
	}
}

func (m *ConManager) Create(args CreateParams) (c *Container, err error) {
	// checks
	name := args.Name
	image := args.Image
	if name == "default" {
		return nil, errors.New("invalid container name")
	}
	if _, ok := m.GetByName(name); ok {
		return nil, errors.New("container already exists")
	}

	// image defaults
	if image.Variant == "" {
		image.Variant = "default"
	}
	if image.Arch == "" {
		image.Arch, err = getDefaultLxcArch()
		if err != nil {
			return
		}
	}

	id := ulid.Make().String()
	logrus.WithFields(logrus.Fields{
		"id":   id,
		"name": name,
	}).Info("creating container")
	record := ContainerRecord{
		ID:    id,
		Name:  name,
		Image: image,

		Running:  false,
		Deleting: false,
	}

	c, err = m.newContainer(record.ID, record.Name, record.Image)
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			err2 := c.Delete()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container creation")
			}
		}
	}()

	// TODO select repo and mirror
	options := lxc.TemplateOptions{
		Template: "download",
		Backend:  lxc.Directory,
		Distro:   image.Distro,
		Release:  image.Version,
		Arch:     image.Arch,
		Variant:  image.Variant,
	}

	err = c.c.Create(options)
	if err != nil {
		return
	}

	// persist
	err = c.persist()
	if err != nil {
		return
	}

	// delete the config file
	err = os.RemoveAll(path.Join(m.lxcDir, c.Name))
	if err != nil {
		return
	}

	// start
	err = c.Start()
	if err != nil {
		return
	}

	// get host user
	hostUser, err := m.host.GetUser()
	if err != nil {
		return
	}
	uid, err := strconv.Atoi(hostUser.Uid)
	if err != nil {
		return
	}

	// tell agent to run setup
	logrus.WithFields(logrus.Fields{
		"uid":      uid,
		"username": hostUser.Username,
	}).Info("running initial setup")
	err = c.Agent().InitialSetup(agent.InitialSetupArgs{
		Username: hostUser.Username,
		Uid:      uid,
		Password: args.UserPassword,
	})
	if err != nil {
		return
	}

	return
}
