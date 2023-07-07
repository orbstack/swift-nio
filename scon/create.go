package main

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

const (
	ipPollInterval = 100 * time.Millisecond
)

var (
	// min 2 chars, disallows hidden files (^.)
	containerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)
	// .orb.internal domains, plus "default" special ssh name
	containerNameBlacklist = []string{"default", "vm", "host", "services", "gateway"}
)

type CreateParams struct {
	Name  string
	Image types.ImageSpec

	UserPassword string
}

func validateContainerName(name string) error {
	if !containerNameRegex.MatchString(name) || slices.Contains(containerNameBlacklist, name) {
		return fmt.Errorf("invalid machine name '%s'", name)
	}
	return nil
}

func (m *ConManager) beginCreate(args CreateParams) (*Container, *types.ImageSpec, error) {
	if m.stopping {
		return nil, nil, errors.New("machine manager is stopping")
	}

	// checks
	name := args.Name
	image := args.Image
	err := validateContainerName(name)
	if err != nil {
		return nil, nil, err
	}
	if _, err := m.GetByName(name); err == nil {
		return nil, nil, fmt.Errorf("machine already exists: '%s'", name)
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
		State: types.ContainerStateCreating,
	}

	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	c, _, err := m.restoreOneLocked(&record, false)
	if err != nil {
		return nil, nil, fmt.Errorf("restore: %w", err)
	}

	return c, &image, nil
}

func (m *ConManager) Create(args CreateParams) (c *Container, err error) {
	c, image, err := m.beginCreate(args)
	if err != nil {
		return
	}

	defer func() {
		if err != nil {
			err2 := c.deleteInternal()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container creation")
			}
		}
	}()

	err = m.makeRootfsWithImage(*image, c.Name, c.rootfsDir)
	if err != nil {
		err = fmt.Errorf("make rootfs: %w", err)
		return
	}

	// start
	c.mu.Lock()
	err = c.startLocked(true /* isInternal */)
	c.mu.Unlock()
	if err != nil {
		err = fmt.Errorf("start: %w", err)
		return
	}

	// setup
	err = c.setupInitial(args)
	if err != nil {
		err = fmt.Errorf("setup: %w", err)
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

	logrus.WithField("container", c.Name).Info("container created")
	return
}

func (c *Container) setupInitial(args CreateParams) error {
	// get host user
	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	// get host timezone
	hostTimezone, err := c.manager.host.GetTimezone()
	if err != nil {
		return fmt.Errorf("get timezone: %w", err)
	}

	// get git configs
	var gitConfigs agent.BasicGitConfigs
	hostGitConfigs, err := c.manager.host.GetGitConfig()
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
	ips, err = c.waitIPAddrs(startStopTimeout)
	if err != nil {
		return err
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
			Distro:          c.Image.Distro,
			Timezone:        hostTimezone,
			BasicGitConfigs: gitConfigs,
		})
	})
	if err != nil {
		return fmt.Errorf("do initial setup: %w", err)
	}

	return nil
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
