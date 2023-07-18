package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/dockerdb"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/inotify"
)

const (
	ContainerDocker   = "docker"
	ContainerIDDocker = "01GQQVF6C60000000000DOCKER"

	// takes ~3 ms to unfreeze
	dockerFreezeDebounce = 2 * time.Second

	dockerNfsDebounce = 250 * time.Millisecond

	nfsDockerSubdir = "docker/volumes"
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
	cm.set("lxc.net.0.ipv4.address", netconf.SconDockerIP4+"/24")
	cm.set("lxc.net.0.ipv4.gateway", netconf.SconGatewayIP4)
	cm.set("lxc.net.0.ipv6.address", netconf.SconDockerIP6+"/64")
	cm.set("lxc.net.0.ipv6.gateway", netconf.SconGatewayIP6)

	// attach Docker vmnet to machine's netns
	// inside machine, we'll attach it to the Docker bridge
	/*
		cm.set("lxc.net.1.type", "phys")
		cm.set("lxc.net.1.link", ifVmnetDocker)
		cm.set("lxc.net.1.flags", "up")
	*/

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
		// disable IPv6 by default
		"ipv6": false,
		// most reliable, and fast on btrfs due to reflinks
		"storage-driver": "overlay2",
		// match our MTU
		"mtu": c.manager.net.mtu,
		// compat issue with docker-compose v1 / Lando: https://github.com/orbstack/orbstack/issues/376
		/*
			"default-network-opts": map[string]any{
				"bridge": map[string]any{
					"com.docker.network.driver.mtu": strconv.Itoa(c.manager.net.mtu),
				},
			},
		*/

		// 192.168 tends to be least used according to GitHub Code Search scraping
		// here, we optimize for both the .0 and .1 /23 pair between low
		// first two are flipped: for the default net, we prioritize the .0 (base) part being lower. 60% weight for first, 40% total
		// our logic: 172.x will prob conflict anyway
		// let's go with /24 to optimize for min conflicts in common case (<255 containers).
		// users can change if needed. this is also simpler for devs
		"bip": "192.168.215.1/24",
		// change default addrs to minimize conflicts with other networks
		"default-address-pools": []map[string]any{
			// custom: first 32 from script (25 - 2)
			{"base": "192.168.215.0/24", "size": 24},
			{"base": "192.168.228.0/24", "size": 24},
			// reserved for possible future machines use
			//{"base": "192.168.243.0/24", "size": 24},
			{"base": "192.168.247.0/24", "size": 24},
			{"base": "192.168.207.0/24", "size": 24},
			{"base": "192.168.167.0/24", "size": 24},
			{"base": "192.168.107.0/24", "size": 24},
			{"base": "192.168.237.0/24", "size": 24},
			{"base": "192.168.148.0/24", "size": 24},
			{"base": "192.168.214.0/24", "size": 24},
			{"base": "192.168.165.0/24", "size": 24},
			{"base": "192.168.227.0/24", "size": 24},
			{"base": "192.168.181.0/24", "size": 24},
			{"base": "192.168.158.0/24", "size": 24},
			{"base": "192.168.117.0/24", "size": 24},
			{"base": "192.168.155.0/24", "size": 24},
			{"base": "192.168.194.0/24", "size": 24},
			{"base": "192.168.147.0/24", "size": 24},
			{"base": "192.168.229.0/24", "size": 24},
			{"base": "192.168.183.0/24", "size": 24},
			{"base": "192.168.156.0/24", "size": 24},
			{"base": "192.168.97.0/24", "size": 24},
			{"base": "192.168.171.0/24", "size": 24},
			{"base": "192.168.186.0/24", "size": 24},
			// removed: < 100 even number is prob common
			//{"base": "192.168.94.0/24", "size": 24},
			{"base": "192.168.216.0/24", "size": 24},
			{"base": "192.168.242.0/24", "size": 24},
			{"base": "192.168.166.0/24", "size": 24},
			{"base": "192.168.239.0/24", "size": 24},
			{"base": "192.168.223.0/24", "size": 24},
			{"base": "192.168.164.0/24", "size": 24},
			{"base": "192.168.163.0/24", "size": 24},
			{"base": "192.168.172.0/24", "size": 24},
			{"base": "192.168.138.0/24", "size": 24},

			// Docker defaults for overflow (and compat, if explicit subnet is specified for a network)
			{"base": "172.17.0.0/16", "size": 16},
			{"base": "172.18.0.0/16", "size": 16},
			{"base": "172.19.0.0/16", "size": 16},
			{"base": "172.20.0.0/14", "size": 16},
			{"base": "172.24.0.0/14", "size": 16},
			{"base": "172.28.0.0/14", "size": 16},
			// remove the 192.168 pool to avoid conflicts
			//{"base": "192.168.0.0/16", "size": 20},
		},

		// fast shutdown. people usually don't care
		"shutdown-timeout": 1,
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

	// iff IPv6 is enabled and user did not set a CIDR, set our default
	// otherwise keep it unset to avoid adding IPv6 to bridge IPAM
	if ipv6, ok := config["ipv6"].(bool); ok && ipv6 {
		if _, ok := config["fixed-cidr-v6"]; !ok {
			config["fixed-cidr-v6"] = "fd07:b51a:cc66:0001::/64"
		}
	}

	rootfs := conf.C().DockerRootfs
	// prevent symlink escape
	fs, err := securefs.NewFS(rootfs)
	if err != nil {
		return err
	}
	defer fs.Close()

	// check for possible conflict between user-created bridge nets and default (bip)
	if bip, ok := config["bip"].(string); ok && bip != "" {
		conflictNet, err := dockerdb.CheckBipNetworkConflict(conf.C().DockerDataDir+"/network/files/local-kv.db", bip)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			logrus.WithError(err).Error("failed to check docker bip conflict")
			conflictNet = nil
		}

		// to prevent infinite loop: if flag exists, delete it and bail out
		// we already tried once and it must've failed
		delErr := fs.Remove(agent.DockerMigrationFlag)
		if conflictNet != nil && errors.Is(delErr, os.ErrNotExist) {
			// migration needed
			logrus.WithField("bip", bip).WithField("conflictNet", conflictNet).Warn("docker bip conflict detected, migrating")

			// create flag file with orig config
			origConfig := config
			origConfigBytes, err := json.Marshal(&origConfig)
			if err != nil {
				return err
			}
			err = fs.WriteFile(agent.DockerMigrationFlag, []byte(origConfigBytes), 0644)
			if err != nil {
				return err
			}

			// use temporary bip to avoid conflict so we can start dockerd
			config["bip"] = agent.DockerMigrationBip

			bipPrefix, err := netip.ParsePrefix(bip)
			if err != nil {
				return err
			}

			// remove conflicting pools so we don't migrate to those and cause more conflicts
			if pools, ok := config["default-address-pools"].([]map[string]any); ok {
				var newPools []map[string]any
				for _, pool := range pools {
					// parse base
					if base, ok := pool["base"].(string); ok {
						basePrefix, err := netip.ParsePrefix(base)
						if err != nil {
							return err
						}

						// add if not conflict
						if !basePrefix.Overlaps(bipPrefix) {
							newPools = append(newPools, pool)
						}
					}
				}

				config["default-address-pools"] = newPools
			}
		}
	}

	configBytes, err := json.Marshal(&config)
	if err != nil {
		return err
	}
	err = fs.WriteFile("/etc/docker/daemon.json", configBytes, 0644)
	if err != nil {
		return err
	}

	// symlink ~/.docker/certs.d. host ensures this exists
	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	_ = fs.Remove("/etc/docker/certs.d")
	err = fs.Symlink(mounts.Virtiofs+hostUser.HomeDir+"/.docker/certs.d", "/etc/docker/certs.d")
	if err != nil {
		return fmt.Errorf("link certs: %w", err)
	}

	// write certs
	err = c.manager.getAndWriteCerts(fs, "/etc/ssl/certs")
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
			isIdle, err = a.DockerCheckIdle()
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
	// slow, so use async if stopping (b/c we know it doesn't matter at that point)
	isAsync := c.manager.stopping
	err := c.manager.host.ClearDockerState(isAsync)
	if err != nil {
		return fmt.Errorf("clear docker state: %w", err)
	}

	return nil
}

type DockerProxy struct {
	container *Container
	manager   *ConManager
	l         net.Listener
}

func (m *ConManager) startDockerProxy() error {
	l, err := netx.ListenTCP("tcp4", &net.TCPAddr{
		// NIC interface, port 2375
		IP:   util.DefaultAddress4(),
		Port: ports.GuestDocker,
	})
	if err != nil {
		return err
	}

	c, err := m.GetByID(ContainerIDDocker)
	if err != nil {
		return err
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
		return a.DockerWaitStart()
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
		return a.DockerHandleConn(conn)
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
	// create docker data volumes dir so we can watch it with inotify once docker starts first time
	dockerVolDir := conf.C().DockerDataDir + "/volumes"
	err := os.MkdirAll(dockerVolDir, 0755)
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
			nfsSubDst := nfsDockerSubdir + "/" + vol
			err := mountOneNfs(dataSrc, nfsSubDst)
			if err != nil {
				return err
			}
		}

		// remove old volumes
		for _, vol := range removed {
			nfsSubDst := nfsDockerSubdir + "/" + vol
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
