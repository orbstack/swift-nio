package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

const (
	ContainerDocker = "docker"

	// takes ~3 ms to unfreeze
	dockerFreezeDelay  = 2 * time.Second
	dockerStartPoll    = 500 * time.Millisecond
	dockerStartTimeout = 15 * time.Second

	dockerNfsDebounce = 250 * time.Millisecond
)

var (
	dockerContainerRecord = types.ContainerRecord{
		ID:   "01GQQVF6C60000000000DOCKER",
		Name: ContainerDocker,
		Image: types.ImageSpec{
			Distro:  images.ImageDocker,
			Version: "latest",
			Arch:    images.NativeArch(),
			Variant: "default",
		},
		Builtin:  true,
		Running:  true,
		Deleting: false,
	}
)

type DockerDaemonFeatures struct {
	Buildkit bool `json:"buildkit"`
}

type DockerDaemonConfig struct {
	Features      DockerDaemonFeatures `json:"features"`
	IPv6          bool                 `json:"ipv6"`
	FixedCIDRv6   string               `json:"fixed-cidr-v6"`
	StorageDriver string               `json:"storage-driver"`
	MTU           int                  `json:"mtu"`
}

type DockerHooks struct {
}

func (h *DockerHooks) Config(c *Container, set func(string, string)) (string, error) {
	// env from Docker
	set("lxc.environment", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")

	// dind does some setup and mounts
	set("lxc.init.cmd", "/usr/bin/docker-init -- /docker-entrypoint.sh")

	// mounts
	set("lxc.mount.entry", "none run tmpfs rw,nosuid,nodev,mode=755 0 0")
	// match docker
	set("lxc.mount.entry", "none dev/shm tmpfs rw,nosuid,nodev,noexec,relatime,size=65536k,create=dir 0 0")
	// alternate tmpfs because our /tmp is symlinked to /private/tmp
	set("lxc.mount.entry", "none dockertmp tmpfs rw,nosuid,nodev,nr_inodes=1048576,inode64,create=dir,optional,size=80% 0 0")

	// configure network statically
	set("lxc.net.0.flags", "up")
	set("lxc.net.0.ipv4.address", dockerIP4+"/24")
	set("lxc.net.0.ipv4.gateway", gatewayIP4)
	set("lxc.net.0.ipv6.address", dockerIP6+"/64")
	set("lxc.net.0.ipv6.gateway", gatewayIP6)

	return conf.C().DockerRootfs, nil
}

func (h *DockerHooks) PreStart(c *Container) error {
	// delete pid file if exists
	rootfs := conf.C().DockerRootfs
	os.Remove(filepath.Join(rootfs, "var/run/docker.pid"))

	// generate docker daemon config
	config := DockerDaemonConfig{
		// just to be safe with legacy clients
		Features: DockerDaemonFeatures{
			Buildkit: true,
		},
		// enable IPv6 with NAT66
		IPv6:        true,
		FixedCIDRv6: "fd00:30:32::/64",
		// most reliable, and fast on btrfs due to reflinks
		StorageDriver: "overlay2",
		// match our MTU
		MTU: c.manager.net.mtu,
	}
	configBytes, err := json.Marshal(&config)
	if err != nil {
		return err
	}
	err = os.WriteFile(filepath.Join(rootfs, "etc/docker/daemon.json"), configBytes, 0644)
	if err != nil {
		return err
	}

	return nil
}

func (h *DockerHooks) PostStart(c *Container) error {
	// make a freezer
	freezer := NewContainerFreezer(c, dockerFreezeDelay, func() (bool, error) {
		// [predicate, via agent] check docker API to see if any containers are running
		// if so, don't freeze
		var isIdle bool
		err := c.useAgentInternal(func(a *agent.Client) error {
			var err error
			isIdle, err = a.CheckDockerIdle()
			return err
		}, false)
		if err != nil {
			return false, err
		}
		return isIdle, nil
	})
	c.freezer = freezer

	// trigger an initial freeze once docker starts
	go c.manager.dockerProxy.kickStart(freezer)

	return nil
}

type DockerProxy struct {
	mu        syncx.Mutex
	container *Container
	manager   *ConManager
	l         net.Listener
}

func (m *ConManager) startDockerProxy() error {
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{
		// NIC interface, port 62375
		IP:   util.DefaultAddress4(),
		Port: ports.GuestDocker,
	})
	if err != nil {
		return err
	}

	c, ok := m.GetByName(ContainerDocker)
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
	err := p.waitForStart()
	if err != nil {
		logrus.WithError(err).Error("failed to wait for docker start")
		return
	}

	logrus.Debug("docker started, dropping freezer ref")
	freezer.DecRef()
}

func (p *DockerProxy) waitForStart() error {
	start := time.Now()
	for {
		// check
		err := p.container.UseAgent(func(a *agent.Client) error {
			_, err := a.CheckDockerIdle()
			return err
		})
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrAgentDead) || errors.Is(err, ErrNotRunning) {
			// start timeout
			return err
		}

		logrus.Debug("poll docker start")
		time.Sleep(dockerStartPoll)
		if time.Since(start) > dockerStartTimeout {
			return errors.New("docker start timeout")
		}
	}
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

	p.mu.Lock()

	// start docker container if not running
	if !p.container.Running() {
		logrus.Debug("docker not running, starting")
		err := p.container.Start()
		if err != nil {
			return err
		}

		// wait for start to avoid EOF
		p.mu.Unlock()
		err = p.waitForStart()
		if err != nil {
			return err
		}
		// no point in reclaiming lock if we had an error
		p.mu.Lock()
	}

	// tell agent to handle this conn
	// UseAgent holds freezer ref
	p.mu.Unlock()
	err := p.container.UseAgent(func(a *agent.Client) error {
		return a.HandleDockerConn(conn)
	})
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
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
			return err
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

		added, removed := diffSlices(lastVols, vols)
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
	debounce := syncx.NewFuncDebounce(dockerNfsDebounce, func() {
		err := updateMountsFunc()
		if err != nil {
			logrus.WithError(err).Error("failed to update docker volume mounts")
		}
	})
	debounce.Call()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add(dockerVolDir)
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create != 0 {
				debounce.Call()
			} else if event.Op&fsnotify.Remove != 0 {
				debounce.Call()
			}
		case err := <-watcher.Errors:
			logrus.WithError(err).Error("docker volume watcher error")
		}
	}
}
