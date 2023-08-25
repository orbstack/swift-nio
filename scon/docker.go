package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/docker/libkv/store"
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

	// currently same
	ContainerK8s   = "k8s"
	ContainerIDK8s = "01GQQVF6C60000000000DOCKER"

	// takes ~3 ms to unfreeze
	dockerFreezeDebounce = 2 * time.Second

	dockerNfsDebounce = 250 * time.Millisecond

	nfsDockerSubdir = "docker/volumes"

	maxBuildCacheSize = 80 * 1024 * 1024 * 1024 // 80 GiB
)

var (
	MACAddrDocker         = deriveMacAddress(ContainerIDDocker)
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

// changes here:
//   - removed "health" from config (can't be overridden in custom config map)
//   - removed livenessProbe that uses /health. there's still a readinessProbe
//   - added static NodeHosts to "coredns" ConfigMap (normally added by k3s)
//
//go:embed k8s/orb-coredns.yml
var k8sCorednsYaml []byte

type DockerDaemonFeatures struct {
	Buildkit bool `json:"buildkit"`
}

type DockerHooks struct {
}

type SimplevisorConfig struct {
	Services [][]string `json:"services"`
}

func (h *DockerHooks) createDataDirs() error {
	err := os.MkdirAll(conf.C().DockerDataDir, 0755)
	if err != nil {
		return err
	}
	// and k8s
	err = os.MkdirAll(conf.C().K8sDataDir+"/cni", 0755)
	if err != nil {
		return err
	}
	err = os.MkdirAll(conf.C().K8sDataDir+"/kubelet", 0755)
	if err != nil {
		return err
	}
	err = os.MkdirAll(conf.C().K8sDataDir+"/k3s", 0755)
	if err != nil {
		return err
	}
	err = os.MkdirAll(conf.C().K8sDataDir+"/etc-node", 0755)
	if err != nil {
		return err
	}

	// add customized coredns: healthcheck removed
	// /var/lib/rancher/k3s/server/manifests/coredns.yaml
	err = os.MkdirAll(conf.C().K8sDataDir+"/k3s/server/manifests", 0755)
	if err != nil {
		return err
	}
	err = os.WriteFile(conf.C().K8sDataDir+"/k3s/server/manifests/orb-coredns.yaml", []byte(k8sCorednsYaml), 0644)
	if err != nil {
		return err
	}

	return nil
}

func (h *DockerHooks) Config(c *Container, cm containerConfigMethods) (string, error) {
	// env from Docker
	cm.set("lxc.environment", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	// use real tmp
	cm.set("lxc.environment", "TMPDIR=/realtmp")
	// disable Go SIGURG preemption to reduce wakeups
	cm.set("lxc.environment", "GODEBUG=asyncpreemptoff=1")

	// dind does some setup and mounts
	cm.set("lxc.init.cmd", "/usr/local/bin/docker-init -- /opt/init")

	// vanity name for k8s node name
	cm.set("lxc.uts.name", "orbstack")

	err := h.createDataDirs()
	if err != nil {
		return "", fmt.Errorf("create data: %w", err)
	}

	// mounts
	// data
	cm.bind(conf.C().DockerDataDir, "/var/lib/docker", "")
	// k8s
	cm.bind(conf.C().K8sDataDir+"/cni", "/var/lib/cni", "")
	cm.bind(conf.C().K8sDataDir+"/kubelet", "/var/lib/kubelet", "")
	cm.bind(conf.C().K8sDataDir+"/k3s", "/var/lib/rancher/k3s", "")
	// for password: /etc/rancher/node/password
	cm.bind(conf.C().K8sDataDir+"/etc-node", "/etc/rancher/node", "")
	// tmp (like dind)
	cm.set("lxc.mount.entry", "none run tmpfs rw,nosuid,nodev,mode=755 0 0")
	// match docker dind
	cm.set("lxc.mount.entry", "none dev/shm tmpfs rw,nosuid,nodev,noexec,relatime,size=65536k,create=dir 0 0")
	// alternate tmpfs because our /tmp is symlinked to /private/tmp
	cm.set("lxc.mount.entry", "none realtmp tmpfs rw,nosuid,nodev,nr_inodes=1048576,inode64,create=dir,optional,size=80% 0 0")
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
	// get disk size for calculating GC policy
	diskSize, err := util.GetDiskSizeBytes(c.manager.dataDir) // rootfs is on overlayfs
	if err != nil {
		return fmt.Errorf("get disk size: %w", err)
	}

	globalLimit := min(diskSize*12/100, maxBuildCacheSize)

	// generate base docker daemon config
	baseFeatures := map[string]any{}
	baseBuilderGC := map[string]any{
		"enabled": true,
		// no defaultKeepStorage. that's only for user default
		// default policies are broken:
		//   - durations are microsecs b/c it assumes seconds unit
		//   - all policies after that are basically the same b/c keepBytes
		// "until" = alias for deprecated "unused-for" (which makes more sense..)
		"policy": []map[string]any{
			// remove cache mounts after 10 days, unless it's really small
			// default includes source.local but that's negligible
			// filters are OR, but until= is special and gets translated to KeepDuration
			// UPDATE: we no longer delete cache mounts because if they're not used to build a layer, they're considered unused, meaning that they always expire after 10d
			//{"filter": []any{"until=240h" /*10d*/, "type=exec.cachemount"}, "keepStorage": "5GB"},

			// remove unused cache after 30 days (avoid size threshold for perf)
			// this is kinda broken - it doesn't clear all that match, only some. need to re-trigger gc to make it go again
			{"all": true, "filter": []any{"until=720h" /*30d*/}, "keepStorage": "0"},
			// global limit = 12% of disk *available to linux*, max 80 GB
			{"all": true, "keepStorage": strconv.FormatUint(globalLimit, 10)},
		},
	}
	baseBuilder := map[string]any{
		"gc": baseBuilderGC,
	}
	config := map[string]any{
		// just to be safe with legacy clients
		"features": baseFeatures,
		// disable IPv6 by default
		"ipv6": false,
		// most reliable, and fast on btrfs due to reflinks
		// no need to set this - it's default since v23, and setting it explicitly breaks containerd snapshotter
		//"storage-driver": "overlay2",
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

		// buildkit builder cache GC
		// default rules are pretty good: https://docs.docker.com/build/cache/garbage-collection/
		"builder": baseBuilder,

		"bip":                   netconf.DockerBIP,
		"default-address-pools": netconf.DockerDefaultAddressPools,

		// fast shutdown. people usually don't care
		"shutdown-timeout": 1,
	}

	// read config overrides from host
	cfg, err := c.manager.host.GetDockerMachineConfig()
	if err != nil {
		return fmt.Errorf("read docker config: %w", err)
	}

	c.manager.k8sEnabled = cfg.K8sEnable
	overrideConfig := cfg.DockerDaemonConfig
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

	// merge builder map
	newBuilder := config["builder"].(map[string]any)
	for k, v := range newBuilder {
		// merge GC map
		if k == "gc" {
			newBuilderGC := v.(map[string]any)
			for k, v := range newBuilderGC {
				baseBuilderGC[k] = v
			}
			v = baseBuilderGC
		}

		baseBuilder[k] = v
	}
	config["builder"] = baseBuilder

	// iff IPv6 is enabled and user did not set a CIDR, set our default
	// otherwise keep it unset to avoid adding IPv6 to bridge IPAM
	if ipv6, ok := config["ipv6"].(bool); ok && ipv6 {
		if _, ok := config["fixed-cidr-v6"]; !ok {
			config["fixed-cidr-v6"] = "fd07:b51a:cc66:1::/64"
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
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, store.ErrKeyNotFound) {
			logrus.WithError(err).Error("failed to check docker bip conflict")
			conflictNet = nil
		}

		// to prevent infinite loop: if flag exists, delete it and bail out
		// we already tried once and it must've failed
		delErr := fs.Remove(agent.DockerNetMigrationFlag)
		if conflictNet != nil && errors.Is(delErr, os.ErrNotExist) {
			// migration needed
			logrus.WithField("bip", bip).WithField("conflictNet", conflictNet).Warn("docker bip conflict detected, migrating")

			// create flag file with orig config
			origConfig := config
			origConfigBytes, err := json.Marshal(&origConfig)
			if err != nil {
				return err
			}
			err = fs.WriteFile(agent.DockerNetMigrationFlag, []byte(origConfigBytes), 0644)
			if err != nil {
				return err
			}

			// use temporary bip to avoid conflict so we can start dockerd
			config["bip"] = agent.DockerNetMigrationBip

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

	// get host timezone
	hostTimezone, err := c.manager.host.GetTimezone()
	if err != nil {
		return fmt.Errorf("get timezone: %w", err)
	}
	// create localtime symlink
	_ = fs.Remove("/etc/localtime")
	err = fs.Symlink("/usr/share/zoneinfo/"+hostTimezone, "/etc/localtime")
	if err != nil {
		logrus.WithError(err).Error("failed to symlink localtime")
	}

	svConfig := SimplevisorConfig{
		Services: [][]string{
			{"dockerd", "--host-gateway-ip=" + netconf.VnetHostNatIP4, "--userland-proxy-path", mounts.Pstub},
		},
	}
	// add k8s service
	if c.manager.k8sEnabled {
		k8sCmd := []string{
			"k3s", "server",
			// ddesktop has no metrics server
			// users may want their own ingress (e.g. nginx) - don't be opinionated
			// coredns is customized to remove health check
			"--disable", "metrics-server,traefik,coredns",
			"--https-listen-port", strconv.Itoa(ports.HostKubernetes),
			"--lb-server-port", strconv.Itoa(ports.HostKubernetes + 1),
			"--docker",
			"--container-runtime-endpoint", "/var/run/docker.sock",
			"--protect-kernel-defaults",
			"--flannel-backend", "host-gw",
			"--cluster-cidr", netconf.K8sClusterCIDR,
			"--service-cidr", netconf.K8sServiceCIDR,
			"--kube-controller-manager-arg", "node-cidr-mask-size=" + netconf.K8sNodeCIDRMaskSize,
			"--write-kubeconfig", "/run/kubeconfig.yml",
		}
		if conf.Debug() {
			k8sCmd = append(k8sCmd, "--enable-pprof")
		}
		svConfig.Services = append(svConfig.Services, k8sCmd)
	}
	// set simplevisor config
	svConfigJson, err := json.Marshal(&svConfig)
	if err != nil {
		return err
	}
	err = c.setLxcConfig("lxc.environment", "SIMPLEVISOR_CONFIG="+string(svConfigJson))
	if err != nil {
		return fmt.Errorf("set simplevisor config: %w", err)
	}

	// create docker data dir in case it was deleted
	err = h.createDataDirs()
	if err != nil {
		return fmt.Errorf("create data: %w", err)
	}

	return nil
}

func (h *DockerHooks) PostStart(c *Container) error {
	// docker-init oom score adj
	// dockerd's score is set via cmdline argument
	initPid := c.initPid
	if initPid != 0 {
		err := os.WriteFile("/proc/"+strconv.Itoa(initPid)+"/oom_score_adj", []byte(util.OomScoreAdjCriticalGuest), 0644)
		if err != nil {
			return err
		}
	}

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

	// prevent freeze if k8s enabled
	// too complicated to freeze it due to async pod lifecycle
	if c.manager.k8sEnabled {
		freezer.incRefCLocked()
	}

	// trigger an initial freeze once docker starts
	go c.manager.dockerProxy.kickStart(freezer)

	return nil
}

func (h *DockerHooks) PostStop(c *Container) error {
	// clear mDNS registry
	c.manager.net.mdnsRegistry.ClearContainers()

	// slow, so use async if stopping (b/c we know it doesn't matter at that point)
	isAsync := c.manager.stopping
	err := c.manager.host.ClearDockerState(isAsync)
	if err != nil {
		return fmt.Errorf("clear docker state: %w", err)
	}

	// clear blocked iptables forwards
	err = c.manager.net.ClearIptablesForwardBlocks()
	if err != nil {
		return fmt.Errorf("clear iptables: %w", err)
	}

	return nil
}

type DockerProxy struct {
	container *Container
	manager   *ConManager
	l         net.Listener
}

func (m *ConManager) startDockerProxy() error {
	l, err := netx.ListenTCP("tcp", &net.TCPAddr{
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
			err := m.nfsForAll.MountBind(dataSrc, nfsSubDst)
			if err != nil {
				return err
			}
		}

		// remove old volumes
		for _, vol := range removed {
			nfsSubDst := nfsDockerSubdir + "/" + vol
			err := m.nfsForAll.Unmount(nfsSubDst)
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
