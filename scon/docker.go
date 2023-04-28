package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/inotify"
)

const (
	ContainerDocker   = "docker"
	ContainerIDDocker = "01GQQVF6C60000000000DOCKER"

	// takes ~3 ms to unfreeze
	dockerFreezeDebounce = 2 * time.Second

	dockerNfsDebounce = 250 * time.Millisecond
)

var (
	dockerContainerRecord = types.ContainerRecord{
		ID:   ContainerIDDocker,
		Name: ContainerDocker,
		Image: types.ImageSpec{
			Distro:  images.ImageDocker,
			Version: "latest",
			Arch:    images.NativeArch(),
			Variant: "default",
		},
		Builtin: true,
		State:   types.ContainerStateRunning,
	}
)

type DockerDaemonFeatures struct {
	Buildkit bool `json:"buildkit"`
}

type DockerHooks struct {
}

func (h *DockerHooks) Config(c *Container, cm containerConfigMethods) (string, error) {
	// env from Docker
	cm.set("lxc.environment", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")

	// dind does some setup and mounts
	cm.set("lxc.init.cmd", "/usr/bin/docker-init -- /docker-entrypoint.sh")

	// create docker data dir in case it was deleted
	err := os.MkdirAll(conf.C().DockerDataDir, 0755)
	if err != nil {
		return "", fmt.Errorf("create docker data: %w", err)
	}

	// mounts
	// data
	cm.bind(conf.C().DockerDataDir, "/var/lib/docker", "")
	// tmp (like dind)
	cm.set("lxc.mount.entry", "none run tmpfs rw,nosuid,nodev,mode=755 0 0")
	// match docker dind
	cm.set("lxc.mount.entry", "none dev/shm tmpfs rw,nosuid,nodev,noexec,relatime,size=65536k,create=dir 0 0")
	// alternate tmpfs because our /tmp is symlinked to /private/tmp
	cm.set("lxc.mount.entry", "none dockertmp tmpfs rw,nosuid,nodev,nr_inodes=1048576,inode64,create=dir,optional,size=80% 0 0")
	// extra linked path: /System
	cm.bind(mounts.Virtiofs+"/System", "/System", "")

	// configure network statically
	cm.set("lxc.net.0.flags", "up")
	cm.set("lxc.net.0.ipv4.address", dockerIP4+"/24")
	cm.set("lxc.net.0.ipv4.gateway", gatewayIP4)
	cm.set("lxc.net.0.ipv6.address", dockerIP6+"/64")
	cm.set("lxc.net.0.ipv6.gateway", gatewayIP6)

	return conf.C().DockerRootfs, nil
}

func (h *DockerHooks) PreStart(c *Container) error {
	// generate base docker daemon config
	baseFeatures := map[string]any{
		"buildkit": true,
	}
	config := map[string]any{
		// just to be safe with legacy clients
		"features": baseFeatures,
		// enable IPv6 with NAT66
		"ipv6":          true,
		"fixed-cidr-v6": "fd00:30:32::/64",
		// most reliable, and fast on btrfs due to reflinks
		"storage-driver": "overlay2",
		// match our MTU
		"mtu": c.manager.net.mtu,
		// TODO: merge with user object
		"default-network-opts": map[string]any{
			"bridge": map[string]any{
				"com.docker.network.driver.mtu": strconv.Itoa(c.manager.net.mtu),
			},
		},
	}

	// read config overrides from host
	overrideConfig, err := c.manager.host.ReadDockerDaemonConfig()
	if err != nil {
		return fmt.Errorf("read docker config: %w", err)
	}
	overrideConfig = strings.TrimSpace(overrideConfig)
	if overrideConfig != "" {
		// write as override
		err = json.Unmarshal([]byte(overrideConfig), &config)
		if err != nil {
			return fmt.Errorf("parse docker config: %w", err)
		}
	}

	// merge features map
	newFeatures := config["features"].(map[string]any)
	for k, v := range newFeatures {
		baseFeatures[k] = v
	}
	config["features"] = baseFeatures

	configBytes, err := json.Marshal(&config)
	if err != nil {
		return err
	}
	rootfs := conf.C().DockerRootfs
	err = os.WriteFile(rootfs+"/etc/docker/daemon.json", configBytes, 0644)
	if err != nil {
		return err
	}

	// symlink ~/.docker/certs.d. host ensures this exists
	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	certsDLink := rootfs + "/etc/docker/certs.d"
	_ = os.Remove(certsDLink)
	err = os.Symlink(mounts.Virtiofs+hostUser.HomeDir+"/.docker/certs.d", certsDLink)
	if err != nil {
		return fmt.Errorf("link certs: %w", err)
	}

	// write certs
	certsDir := rootfs + "/etc/ssl/certs"
	err = c.manager.getAndWriteCerts(certsDir)
	if err != nil {
		return fmt.Errorf("write certs: %w", err)
	}

	// create docker data dir in case it was deleted
	err = os.MkdirAll(conf.C().DockerDataDir, 0755)
	if err != nil {
		return fmt.Errorf("create docker data: %w", err)
	}

	return nil
}

func (h *DockerHooks) PostStart(c *Container) error {
	// make a freezer
	freezer := NewContainerFreezer(c, dockerFreezeDebounce, func() (bool, error) {
		// [predicate, via agent] check docker API to see if any containers are running
		// if so, don't freeze
		var isIdle bool
		// freezer operates under container lock
		err := c.useAgentInternal(func(a *agent.Client) error {
			var err error
			isIdle, err = a.CheckDockerIdle()
			return err
		}, /*needFreezerRef*/ false /*needLock*/, false)
		if err != nil {
			return false, err
		}
		return isIdle, nil
	})
	c.freezer.Store(freezer)

	// trigger an initial freeze once docker starts
	go c.manager.dockerProxy.kickStart(freezer)

	return nil
}

func (h *DockerHooks) PostStop(c *Container) error {
	err := c.manager.host.ClearFsnotifyRefs()
	if err != nil {
		return fmt.Errorf("clear refs: %w", err)
	}

	return nil
}

type DockerProxy struct {
	container *Container
	manager   *ConManager
	l         net.Listener
}

func (m *ConManager) startDockerProxy() error {
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{
		// NIC interface, port 2375
		IP:   util.DefaultAddress4(),
		Port: ports.GuestDocker,
	})
	if err != nil {
		return err
	}

	c, ok := m.GetByID(ContainerIDDocker)
	if !ok {
		return errors.New("docker container not found")
	}

	proxy := &DockerProxy{
		manager:   m,
		container: c,
		l:         l,
	}
	m.dockerProxy = proxy

	go runOne("Docker proxy", proxy.Run)
	return nil
}

func (p *DockerProxy) kickStart(freezer *Freezer) {
	logrus.Debug("waiting for docker start")
	// this fails if agent socket is closed
	err := p.container.UseAgent(func(a *agent.Client) error {
		return a.WaitForDockerStart()
	})
	if err != nil {
		logrus.WithError(err).Error("failed to wait for docker start")
		return
	}

	logrus.Debug("docker started, dropping freezer ref")
	freezer.DecRef()
}

func (p *DockerProxy) Run() error {
	for {
		conn, err := p.l.Accept()
		if err != nil {
			return err
		}

		go func() {
			err := p.handleConn(conn)
			if err != nil {
				logrus.WithError(err).Error("failed to handle docker connection")
			}
		}()
	}
}

func (p *DockerProxy) handleConn(conn net.Conn) error {
	defer conn.Close()

	// start docker container if not running
	if !p.container.Running() {
		logrus.Debug("docker not running, starting")
		err := p.container.Start()
		if err != nil {
			return err
		}
	}

	// tell agent to handle this conn
	// UseAgent holds freezer ref
	// this also waits for docker start on the agent side, so no need to call waitStart
	err := p.container.UseAgent(func(a *agent.Client) error {
		return a.HandleDockerConn(conn)
	})
	if err != nil {
		return err
	}
	// after the RPC call returns, we know the conn is closed

	return nil
}

func (p *DockerProxy) Close() error {
	return p.l.Close()
}

func (m *ConManager) runDockerNFS() error {
	dockerVolDir := conf.C().DockerDataDir + "/volumes"
	err := os.MkdirAll(dockerVolDir, 0755)
	if err != nil {
		return err
	}

	nfsSubdir := "docker/volumes"
	err = os.MkdirAll(conf.C().NfsRootRW+"/"+nfsSubdir, 0755)
	if err != nil {
		return err
	}

	lastVols := []string{}
	updateMountsFunc := func() error {
		// get all volumes
		volEntries, err := os.ReadDir(dockerVolDir)
		if err != nil {
			// if doesn't exist, then assume empty (e.g. just started or just deleted)
			if errors.Is(err, os.ErrNotExist) {
				volEntries = nil
			} else {
				return err
			}
		}

		vols := filterMapSlice(volEntries, func(entry fs.DirEntry) (string, bool) {
			if entry.IsDir() {
				// make sure it has _data
				_, err := os.Stat(dockerVolDir + "/" + entry.Name() + "/_data")
				if err != nil {
					return "", false
				}

				return entry.Name(), true
			} else {
				return "", false
			}
		})

		added, removed := util.DiffSlices(lastVols, vols)
		lastVols = vols

		// add new volumes
		for _, vol := range added {
			dataSrc := dockerVolDir + "/" + vol + "/_data"
			nfsSubDst := "docker/volumes/" + vol
			err := mountOneNfs(dataSrc, nfsSubDst)
			if err != nil {
				return err
			}
		}

		// remove old volumes
		for _, vol := range removed {
			nfsSubDst := "docker/volumes/" + vol
			err := unmountOneNfs(nfsSubDst)
			if err != nil {
				return err
			}
		}

		return nil
	}
	// TODO: use WaitForPathExist w/ timeout so we don't need debounce to wait for _data here
	debounce := syncx.NewFuncDebounce(dockerNfsDebounce, func() {
		err := updateMountsFunc()
		if err != nil {
			logrus.WithError(err).Error("failed to update docker volume mounts")
		}
	})
	debounce.Call()

	watcher, err := inotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.AddWatch(dockerVolDir, inotify.InCreate|inotify.InDelete)
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Event:
			if event.Mask&inotify.InCreate != 0 {
				debounce.Call()
			} else if event.Mask&inotify.InDelete != 0 {
				debounce.Call()
			}
		case err := <-watcher.Error:
			logrus.WithError(err).Error("docker volume watcher error")
		}
	}
}
