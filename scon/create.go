package main

import (
	"errors"
	"regexp"
	"strconv"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
)

var (
	containerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

type CreateParams struct {
	Name  string
	Image types.ImageSpec

	UserPassword string
}

func (m *ConManager) Create(args CreateParams) (c *Container, err error) {
	if m.stopping {
		return nil, errors.New("machine manager is stopping")
	}

	// checks
	name := args.Name
	image := args.Image
	if name == "default" {
		return nil, errors.New("invalid machine name")
	}
	if !containerNamePattern.MatchString(name) {
		return nil, errors.New("invalid machine name")
	}
	if _, ok := m.GetByName(name); ok {
		return nil, errors.New("machine already exists")
	}

	// image defaults
	if image.Version == "" {
		image.Version = images.ImageToLatestVersion[image.Distro]
	}
	if image.Variant == "" {
		var ok bool
		image.Variant, ok = images.ImageToDefaultVariant[image.Distro]
		if !ok {
			image.Variant = "default"
		}
	}
	if image.Arch == "" {
		image.Arch = images.NativeArch()
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

	// get host timezone
	hostTimezone, err := m.host.GetTimezone()
	if err != nil {
		return
	}

	// get git configs
	var gitConfigs agent.BasicGitConfigs
	hostGitConfigs, err := m.host.GetGitConfig()
	if err != nil {
		logrus.WithError(err).Warn("failed to get host git configs")
	} else {
		gitConfigs.Name = hostGitConfigs["user.name"]
		gitConfigs.Email = hostGitConfigs["user.email"]
	}

	// wait for network if this distro needs package installation
	if _, ok := agent.PackageInstallCommands[image.Distro]; ok {
		logrus.WithField("container", c.Name).Info("waiting for network before setup")
		ips, err := c.lxc.WaitIPAddresses(startTimeout)
		if err != nil {
			return nil, err
		}
		logrus.WithField("container", c.Name).WithField("ips", ips).Info("network is up")
	}

	// tell agent to run setup
	logrus.WithFields(logrus.Fields{
		"uid":      uid,
		"username": hostUser.Username,
	}).Info("running initial setup")
	err = c.Agent().InitialSetup(agent.InitialSetupArgs{
		Username:        hostUser.Username,
		Uid:             uid,
		Password:        args.UserPassword,
		Distro:          image.Distro,
		Timezone:        hostTimezone,
		BasicGitConfigs: gitConfigs,
	})
	if err != nil {
		return
	}

	// set as last container
	go m.db.SetLastContainerID(c.ID)

	return
}
