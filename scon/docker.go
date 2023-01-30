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
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

const (
	//TODO longer for prod
	dockerFreezeDelay = 3 * time.Second
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
	mu       sync.Mutex
	manager  *ConManager
	l        net.Listener
	numConns int
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

	proxy := &DockerProxy{
		manager: m,
		l:       l,
	}
	m.dockerProxy = proxy

	return proxy.Run()
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
	c, ok := p.manager.GetByName("docker")
	if !ok {
		return errors.New("docker container not found")
	}
	err := c.Start()
	if err != nil {
		return err
	}

	defer func() {
		p.numConns--
		if p.numConns == 0 {
			// no more connections, freeze container
			go func() {
				time.Sleep(dockerFreezeDelay)
				p.mu.Lock()
				defer p.mu.Unlock()

				if p.numConns > 0 {
					// new connections came in, don't freeze
					return
				}

				c, ok := p.manager.GetByName("docker")
				if !ok {
					logrus.Error("docker container not found")
					return
				}

				err := c.Freeze()
				if err != nil {
					logrus.WithError(err).Error("failed to freeze docker container")
				}
			}()
		}
	}()

	// unfreeze if it's frozen
	err = c.Unfreeze()
	if err != nil && !errors.Is(err, lxc.ErrNotFrozen) {
		return err
	}

	// tell agent to handle this conn
	err = c.Agent().HandleDockerConn(conn)
	if err != nil {
		return err
	}

	return nil
}

func (p *DockerProxy) Close() error {
	return p.l.Close()
}
