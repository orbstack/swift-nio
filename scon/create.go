package main

import (
	"errors"
	"runtime"
	"strconv"
	"strings"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
)

type CreateParams struct {
	Name  string
	Image types.ImageSpec

	UserPassword string
}

func getDefaultLxcArch() string {
	switch runtime.GOARCH {
	case "i386":
		return "i686"
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "arm":
		return "armhf"
	default:
		panic("unsupported architecture")
	}
}

func (m *ConManager) Create(args CreateParams) (c *Container, err error) {
	// checks
	name := args.Name
	image := args.Image
	if name == "default" {
		return nil, errors.New("invalid container name")
	}
	if strings.ContainsRune(name, '/') {
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
		image.Arch = getDefaultLxcArch()
	}

	id := ulid.Make().String()
	logrus.WithFields(logrus.Fields{
		"id":   id,
		"name": name,
	}).Info("creating container")
	record := types.ContainerRecord{
		ID:    id,
		Name:  name,
		Image: image,

		Running:  false,
		Deleting: false,
	}

	c, _, err = m.restoreOne(&record)
	if err != nil {
		return
	}
	c.creating = true

	defer func() {
		if err != nil {
			err2 := c.Delete()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container creation")
			}
		}

		c.creating = false
	}()

	err = m.makeRootfsWithImage(image, c.Name, c.rootfsDir)
	if err != nil {
		return
	}

	// persist
	err = c.persist()
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
		Distro:   image.Distro,
	})
	if err != nil {
		return
	}

	return
}
