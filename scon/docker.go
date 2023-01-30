package main

import (
	"os"
	"path/filepath"

	"github.com/kdrag0n/macvirt/scon/conf"
)

var (
	dockerContainerRecord = ContainerRecord{
		ID:   "01GQQVF6C6VC46KND9TRVFWC1Q",
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
	set("lxc.init.cmd", "/usr/local/bin/dind /usr/bin/docker-init -- dockerd --host=unix:///var/run/docker.sock --host=tcp://0.0.0.0:62375 --tls=false")

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
