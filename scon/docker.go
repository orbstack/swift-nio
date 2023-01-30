package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

const (
	// takes ~3 ms to unfreeze
	dockerFreezeDelay = 2 * time.Second
)

var (
	dockerContainerRecord = ContainerRecord{
		ID:   "01GQQVF6C60000000000DOCKER",
		Name: "docker",
		Image: ImageSpec{
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

type DockerProxy struct {
	mu             sync.Mutex
	container      *Container
	manager        *ConManager
	l              net.Listener
	freezeDebounce syncx.FuncDebounce
	numConns       int
}

func (m *ConManager) runDockerProxy() error {
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{
		// NIC interface, port 62375
		IP:   getDefaultAddress4(),
		Port: ports.GuestDocker,
	})
	if err != nil {
		return err
	}

	c, ok := m.GetByName("docker")
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

	// trigger an initial freeze
	// assume that Docker has started by the time debounce is over
	// if not, it can finish starting next time
	proxy.freezeDebounce.Call()

	return proxy.Run()
}

func (p *DockerProxy) tryFreeze() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	logrus.Trace("considering docker freeze")

	// if new connections came in, don't freeze
	if p.numConns > 0 {
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
	if err != nil {
		return err
	}
	// after the RPC call returns, we know the conn is closed

	return nil
}

func (p *DockerProxy) Close() error {
	return p.l.Close()
}
