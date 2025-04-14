package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/sirupsen/logrus"
)

const (
	ipPollInterval = 100 * time.Millisecond
)

func validateContainerName(name string) error {
	if !types.ContainerNameRegex.MatchString(name) || slices.Contains(types.ContainerNameBlacklist, name) {
		return fmt.Errorf("invalid machine name '%s'", name)
	}
	return nil
}

// returns a Container with an active mutation that must be released after creation
func (m *ConManager) beginCreate(args *types.CreateRequest) (*Container, *types.ImageSpec, error) {
	if m.stopping.Load() {
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

	// for cloud-init, variant must be "cloud", and that variant must be available
	if args.CloudInitUserData != "" {
		if _, ok := images.ImagesWithCloudVariant[image.Distro]; !ok {
			return nil, nil, fmt.Errorf("cloud-init not supported for '%s'", image.Distro)
		}

		image.Variant = "cloud"
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

	// apply version alias
	versionKey := images.ImageVersion{
		Image:   image.Distro,
		Version: image.Version,
	}
	if version, ok := images.ImageVersionAliases[versionKey]; ok {
		image.Version = version
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
		// sanitized by restoreOneLocked
		Config: args.Config,
		State:  types.ContainerStateCreating,
	}

	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	c, _, err := m.restoreOneLocked(&record, true /*isNew*/)
	if err != nil {
		return nil, nil, fmt.Errorf("restore: %w", err)
	}

	return c, &image, nil
}

func (m *ConManager) Create(args *types.CreateRequest) (_ *Container, retErr error) {
	c, image, err := m.beginCreate(args)
	if err != nil {
		return nil, err
	}
	defer c.holds.EndMutation()
	defer func() {
		if retErr != nil {
			err2 := c.deleteInternal()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container creation")
			}
		}
	}()

	// model this as a long-running task
	err = c.jobManager.Run(func(ctx context.Context) error {
		err := c.makeRootfsWithImage(ctx, *image, c.Name, args.CloudInitUserData, args.InternalUseTestCache)
		if ctx.Err() != nil {
			// if canceled, just return that instead of the actual error (e.g. unsquashfs killed)
			return ctx.Err()
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("make rootfs: %w", err)
	}

	// start
	c.mu.Lock()
	err = c.startLocked(true /* isInternal */)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// setup
	setupArgs, err := c.setupInitial(args)
	if err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}

	// reboot NixOS to not run into weird errors (https://github.com/orbstack/macvirt/pull/111#issuecomment-2155174982)
	if c.Image.Distro == images.DistroNixos {
		c.mu.Lock()
		_, err = c.stopLocked(StopOptions{KillProcesses: false, ManagerIsStopping: false})
		c.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("stop (nixos reboot): %w", err)
		}

		c.mu.Lock()
		err = c.startLocked(true /* isInternal */)
		c.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("stop (nixos reboot): %w", err)
		}
	}

	err = c.UseAgent(func(a *agent.Client) error {
		return a.InitialSetupStage2(*setupArgs)
	})
	if err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}

	// also set as default if this is the first container
	if m.CountNonBuiltinContainers() <= 1 {
		err = m.db.SetDefaultContainerID(c.ID)
		if err != nil {
			return nil, fmt.Errorf("set default container: %w", err)
		}
	}

	// add to NFS
	// restoring the container doesn't call this if state=creating
	err = m.onRestoreContainer(c)
	if err != nil {
		return nil, fmt.Errorf("call restore hook: %w", err)
	}

	// we're done creating; set IsCreating to false & persist
	c.isCreating.Store(false)
	err = c.persist()
	if err != nil {
		return nil, fmt.Errorf("finish creation: %w", err)
	}

	logrus.WithField("container", c.Name).Info("container created")
	return c, nil
}

func (c *Container) setupInitial(args *types.CreateRequest) (*agent.InitialSetupArgs, error) {
	// get host user
	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// get host timezone
	hostTimezone, err := c.manager.host.GetTimezone()
	if err != nil {
		return nil, fmt.Errorf("get timezone: %w", err)
	}

	// always wait for network
	// even if we don't need it for setup, it ensures that resolved has started if necesary,
	// and systemctl will work
	logrus.WithField("container", c.Name).Info("waiting for network before setup")
	var ips []string
	ips, err = c.waitIPAddrs(startStopTimeout)
	if err != nil {
		return nil, err
	}
	logrus.WithField("container", c.Name).WithField("ips", ips).Info("network is up")

	// tell agent to run setup
	logrus.WithFields(logrus.Fields{
		"uid":      hostUser.Uid,
		"username": c.config.DefaultUsername,
	}).Info("running initial setup")

	setupArgs := agent.InitialSetupArgs{
		Username:    c.config.DefaultUsername,
		Uid:         hostUser.Uid,
		HostHomeDir: hostUser.HomeDir,

		Password: args.UserPassword,
		Distro:   c.Image.Distro,
		Version:  c.Image.Version,
		Timezone: hostTimezone,
	}

	err = c.UseAgent(func(a *agent.Client) error {
		return a.InitialSetupStage1(setupArgs)
	})
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errors.New("do initial setup: canceled by machine shutdown")
		} else {
			return nil, fmt.Errorf("do initial setup: %w", err)
		}
	}

	return &setupArgs, nil
}

func (c *Container) waitIPAddrs(timeout time.Duration) ([]string, error) {
	start := time.Now()
	for {
		if time.Since(start) > timeout {
			return nil, fmt.Errorf("machine didn't start in %v (missing IP address)", timeout)
		}

		if !c.lxc.Running() {
			return nil, fmt.Errorf("machine crashed on startup")
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

type createDataDirsOptions struct {
	includeRootfsDir bool
	snapshotFromPath string
}

func (c *Container) createDataDirs(opts createDataDirsOptions) error {
	if opts.snapshotFromPath != "" {
		err := c.manager.fsOps.SnapshotSubvolume(opts.snapshotFromPath, c.dataDir)
		if err != nil {
			return err
		}
	} else {
		err := c.manager.fsOps.CreateSubvolumeIfNotExists(c.dataDir)
		if err != nil {
			return err
		}
	}

	if opts.includeRootfsDir {
		err := os.Mkdir(c.rootfsDir, 0o755)
		if err != nil {
			return err
		}
	}

	return nil
}
