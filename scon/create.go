package main

import (
	"errors"
	"net/netip"
	"regexp"
	"time"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/oklog/ulid/v2"
	"github.com/sirupsen/logrus"
)

const (
	ipPollInterval = 100 * time.Millisecond
)

var (
	containerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

type CreateParams struct {
	Name  string
	Image types.ImageSpec

	UserPassword string
}

func (m *ConManager) beginCreate(args CreateParams) (*Container, *types.ImageSpec, error) {
	if m.stopping {
		return nil, nil, errors.New("machine manager is stopping")
	}

	// checks
	name := args.Name
	image := args.Image
	if name == "default" || name == "host" || !containerNamePattern.MatchString(name) {
		return nil, nil, errors.New("invalid machine name")
	}
	if _, ok := m.GetByName(name); ok {
		return nil, nil, errors.New("machine already exists")
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

	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	c, _, err := m.restoreOneLocked(&record, false)
	if err != nil {
		return nil, nil, err
	}
	c.creating = true

	return c, &image, nil
}

func (m *ConManager) Create(args CreateParams) (c *Container, err error) {
	c, image, err := m.beginCreate(args)
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

		c.creating = false
	}()

	err = m.makeRootfsWithImage(*image, c.Name, c.rootfsDir)
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

	// always wait for network
	// even if we don't need it for setup, it ensures that resolved has started if necesary,
	// and systemctl will work
	logrus.WithField("container", c.Name).Info("waiting for network before setup")
	var ips []string
	ips, err = c.waitIPAddrs(startTimeout)
	if err != nil {
		return
	}
	logrus.WithField("container", c.Name).WithField("ips", ips).Info("network is up")

	// tell agent to run setup
	logrus.WithFields(logrus.Fields{
		"uid":      hostUser.Uid,
		"username": hostUser.Username,
	}).Info("running initial setup")
	err = c.UseAgent(func(a *agent.Client) error {
		return a.InitialSetup(agent.InitialSetupArgs{
			Username:    hostUser.Username,
			Uid:         hostUser.Uid,
			HostHomeDir: hostUser.HomeDir,

			Password:        args.UserPassword,
			Distro:          image.Distro,
			Timezone:        hostTimezone,
			BasicGitConfigs: gitConfigs,
		})
	})
	if err != nil {
		return
	}

	// set as last container
	err = m.db.SetLastContainerID(c.ID)
	if err != nil {
		return
	}

	// also set as default if this is the first container
	if m.CountNonBuiltinContainers() <= 1 {
		err = m.db.SetDefaultContainerID(c.ID)
		if err != nil {
			return
		}
	}

	return
}

func (c *Container) waitIPAddrs(timeout time.Duration) ([]string, error) {
	start := time.Now()
	for {
		if time.Since(start) > timeout {
			return nil, errors.New("timed out waiting for network")
		}

		ips, err := c.lxc.IPAddresses()
		if err != nil {
			continue
		}

		// we want both IPv4 and IPv6 to prevent setup failures
		has4 := false
		has6 := false
		for _, ip := range ips {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				return nil, err
			}
			addr = addr.Unmap()

			if addr.Is4() {
				has4 = true
			} else {
				has6 = true
			}
		}
		if has4 && has6 {
			return ips, nil
		}

		time.Sleep(ipPollInterval)
	}
}
