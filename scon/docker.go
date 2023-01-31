package main

import (
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

const (
	ContainerDocker = "docker"

	// takes ~3 ms to unfreeze
	dockerFreezeDelay = 2 * time.Second
	dockerStartPoll   = 500 * time.Millisecond

	dockerNfsDebounce = 250 * time.Millisecond
)

var (
	dockerContainerRecord = types.ContainerRecord{
		ID:   "01GQQVF6C60000000000DOCKER",
		Name: ContainerDocker,
		Image: types.ImageSpec{
			Distro:  ImageDocker,
			Version: "latest",
			Arch:    getDefaultLxcArch(),
			Variant: "default",
		},
		Builtin:  true,
		Running:  true,
		Deleting: false,
	}
)

type DockerHooks struct {
}

func (h *DockerHooks) Config(c *Container, set func(string, string)) (string, error) {
	// env from Docker
	set("lxc.environment", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")

	// dind does some setup and mounts
	set("lxc.init.cmd", "/usr/local/bin/dind /usr/bin/docker-init -- dockerd --host=unix:///var/run/docker.sock --tls=false")

	// mounts
	set("lxc.mount.entry", "none run tmpfs rw,nosuid,nodev,mode=755 0 0")
	// match docker
	set("lxc.mount.entry", "none dev/shm tmpfs rw,nosuid,nodev,noexec,relatime,size=65536k,create=dir 0 0")

	// configure network statically
	set("lxc.net.0.flags", "up")
	// ipv6 is auto-configured by SLAAC, so just set v4
	set("lxc.net.0.ipv4.address", dockerIP4+"/24")
	set("lxc.net.0.ipv4.gateway", gatewayIP4)

	return conf.C().DockerRootfs, nil
}

func (h *DockerHooks) PreStart(c *Container) error {
	// delete pid file if exists
	rootfs := conf.C().DockerRootfs
	os.Remove(filepath.Join(rootfs, "var/run/docker.pid"))

	return nil
}

func (h *DockerHooks) PostStart(c *Container) error {
	// trigger an initial freeze once docker starts
	go c.manager.dockerProxy.kickStart()

	return nil
}

type DockerProxy struct {
	mu             sync.Mutex
	container      *Container
	manager        *ConManager
	l              net.Listener
	freezeDebounce syncx.FuncDebounce
	numConns       int
}

func (m *ConManager) startDockerProxy() error {
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{
		// NIC interface, port 62375
		IP:   getDefaultAddress4(),
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
	proxy.freezeDebounce = syncx.NewFuncDebounce(dockerFreezeDelay, func() {
		err := proxy.tryFreeze()
		if err != nil {
			logrus.WithError(err).Error("failed to freeze docker")
		}
	})
	m.dockerProxy = proxy

	go runOne("Docker proxy", proxy.Run)
	return nil
}

func (p *DockerProxy) kickStart() {
	logrus.Debug("waiting for docker start")
	err := p.waitForStart()
	if err != nil {
		logrus.WithError(err).Error("failed to wait for docker start")
		return
	}

	logrus.Debug("docker started, freezing")
	p.freezeDebounce.Call()
}

func (p *DockerProxy) waitForStart() error {
	// wait for docker to start if container is running
	if !p.container.Running() {
		return nil
	}

	for {
		p.mu.Lock()
		frozen := p.container.IsFrozen()
		p.mu.Unlock()

		if frozen {
			return nil
		}

		// check
		_, err := p.container.Agent().CheckDockerIdle()
		if err == nil {
			return nil
		}

		time.Sleep(dockerStartPoll)
	}
}

func (p *DockerProxy) tryFreeze() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	logrus.Trace("considering docker freeze")

	// if new connections came in, don't freeze
	if p.numConns > 0 {
		return nil
	}

	// if container was stopped, don't freeze
	if !p.container.Running() {
		return nil
	}

	// if already frozen, don't freeze again
	if p.container.IsFrozen() {
		return nil
	}

	// [via agent] check docker API to see if any containers are running
	// if so, don't freeze
	isIdle, err := p.container.Agent().CheckDockerIdle()
	if err != nil {
		return err
	}
	if !isIdle {
		return nil
	}

	// freeze!
	logrus.Trace("freezing docker")
	return p.container.Freeze()
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

	// cancel pending freeze
	p.freezeDebounce.Cancel()

	// start docker container if not running
	err := p.container.Start()
	if err != nil {
		return err
	}

	// increment while locked
	p.numConns++
	defer func() {
		// after conn done, check and trigger a freeze
		p.mu.Lock()
		defer p.mu.Unlock()

		p.numConns--
		logrus.WithField("numConns", p.numConns).Debug("docker conn closed")
		if p.numConns == 0 {
			// no more connections, trigger a pending freeze
			p.freezeDebounce.Call()
		}
		if p.numConns < 0 {
			logrus.Error("docker proxy numConns < 0")
		}
	}()

	// unfreeze if it's frozen
	err = p.container.Unfreeze()
	if err != nil {
		if !errors.Is(err, lxc.ErrNotFrozen) {
			return err
		}
	} else {
		logrus.Trace("unfreezing docker")
	}

	// tell agent to handle this conn
	p.mu.Unlock()
	err = p.container.Agent().HandleDockerConn(conn)
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
