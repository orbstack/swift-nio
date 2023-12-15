package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/docker/libkv/store"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/dockerdb"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol/htypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	ContainerDocker   = "docker"
	ContainerIDDocker = "01GQQVF6C60000000000DOCKER"

	// currently same
	ContainerK8s   = "k8s"
	ContainerIDK8s = "01GQQVF6C60000000000DOCKER"

	// takes ~3 ms to unfreeze
	dockerFreezeDebounce = 2 * time.Second

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

// put them here for obfuscation
var dockerInitCommands = [][]string{
	{"ip", "addr", "add", netconf.SconDockerIP6CIDR, "dev", "eth0"},
	{"ip", "-6", "route", "add", "default", "via", netconf.SconGatewayIP6, "dev", "eth0"},

	// match systemd
	{"mount", "--make-rshared", "/"},

	// compat for kruise expecting containerd OR docker+dockershim: https://github.com/openkruise/kruise/blob/4e80be556726e60f54abaa3e8ba133ce114c4f64/pkg/daemon/criruntime/factory.go#L200
	{"ln", "-sf", "/var/run/k3s/cri-dockerd/cri-dockerd.sock", "/var/run/dockershim.sock"},

	{"iptables", "-t", "nat", "-N", "ORB-PREROUTING"},
	{"iptables", "-t", "nat", "-A", "PREROUTING", "-j", "ORB-PREROUTING"},
	// 172.17.0.1 IP gateway compat. people hard code this...
	{"iptables", "-t", "nat", "-A", "ORB-PREROUTING", "-s", "192.168.215.0/24,192.168.228.0/24,192.168.247.0/24,192.168.207.0/24,192.168.167.0/24,192.168.107.0/24,192.168.237.0/24,192.168.148.0/24,192.168.214.0/24,192.168.165.0/24,192.168.227.0/24,192.168.181.0/24,192.168.158.0/24,192.168.117.0/24,192.168.155.0/24", "-d", "172.17.0.1", "-j", "DNAT", "--to-destination", "198.19.249.2"},
	{"iptables", "-t", "nat", "-N", "ORB-POSTROUTING"},
	{"iptables", "-t", "nat", "-A", "POSTROUTING", "-j", "ORB-POSTROUTING"},
	// "fix" ipv4 docker port forward source IPs. normally docker only does DNAT w/o MASQUERADE to preserve source IP, but our source IP is internal vnet and people expect it to come from the machine like normal linux loopback
	// needed because people make assumptions about source IPs
	// IMPORTANT: we CANNOT masquerade for NAT64 10.183 IP because cfwd needs it as a port scanning signal, and mark will be lost when it reaches the container
	{"iptables", "-t", "nat", "-A", "ORB-POSTROUTING", "-s", "198.19.248.1/32", "!", "-d", "127.0.0.0/8", "!", "-o", "eth0", "-j", "MASQUERADE"},

	{"ip6tables", "-t", "nat", "-N", "ORB-POSTROUTING"},
	{"ip6tables", "-t", "nat", "-A", "POSTROUTING", "-j", "ORB-POSTROUTING"},
	// masquerade outgoing IPv6 traffic to internet, from a ULA that's not ours. fixes kind ipv6 access
	{"ip6tables", "-t", "nat", "-N", "ORB-POSTROUTING-S1"},
	{"ip6tables", "-t", "nat", "-A", "ORB-POSTROUTING-S1", "!", "-s", "fd07:b51a:cc66::/64", "-j", "MASQUERADE"},
	{"ip6tables", "-t", "nat", "-A", "ORB-POSTROUTING", "-s", "fc00::/7", "-o", "eth0", "-j", "ORB-POSTROUTING-S1"},
	// "fix" ipv6 docker port forward source IPs. same logic as v4 - currently useless b/c v6 uses userspace proxy
	{"ip6tables", "-t", "nat", "-A", "ORB-POSTROUTING", "-s", "fd07:b51a:cc66:f0::1/128", "!", "-d", "::1/128", "!", "-o", "eth0", "-j", "MASQUERADE"},

	// TLS proxy: special listener address for TPROXY redirect
	{"ip", "addr", "add", netconf.VnetTlsProxyIP4 + "/32", "dev", "lo"},
	{"ip", "addr", "add", netconf.VnetTlsProxyIP6 + "/128", "dev", "lo"},

	// TLS proxy: loopback routing for connection to TLS proxy
	// busybox only supports table ID < 1024 but kernel can do 32-bit(? or is it just string?)
	{"ip", "rule", "add", "fwmark", agent.TlsProxyLocalRouteMarkStr, "table", "984"},
	{"ip", "route", "add", "local", "default", "dev", "lo", "table", "984"},

	// mixed ipv4 and ipv6 hash set
	{"ipset", "create", agent.IpsetHostBridge4, "hash:ip"},
	{"ipset", "create", agent.IpsetGateway4, "hash:ip"},
	{"ipset", "create", agent.IpsetHostBridge6, "hash:ip", "family", "inet6"},
	{"ipset", "create", agent.IpsetGateway6, "hash:ip", "family", "inet6"},
	// add nat64 IP - always counts as host
	{"ipset", "add", agent.IpsetHostBridge4, netconf.NAT64SourceIP4},

	// prepare chains for TLS proxy
	{"iptables", "-t", "mangle", "-N", "ORB-PREROUTING"},
	{"iptables", "-t", "mangle", "-A", "PREROUTING", "-j", "ORB-PREROUTING"},
	{"iptables", "-t", "mangle", "-N", "ORB-OUTPUT"},
	{"iptables", "-t", "mangle", "-A", "OUTPUT", "-j", "ORB-OUTPUT"},

	// *** TLS proxy (v4)
	// complicated routing to make source IP spoofing work:
	// 1. outgoing socket has SO_MARK set to TlsProxyUpstreamMarkStr
	// 2. on OUTPUT path, save the OUTGOING mark to conntrack metadata for this 5-tuple
	{"iptables", "-t", "mangle", "-A", "ORB-OUTPUT", "-m", "mark", "--mark", agent.TlsProxyUpstreamMarkStr, "-j", "CONNMARK", "--save-mark"},
	// 3. on *input* PREROUTING path, restore the mark from conntrack metadata
	{"iptables", "-t", "mangle", "-A", "ORB-PREROUTING", "-m", "connmark", "--mark", agent.TlsProxyUpstreamMarkStr, "-j", "CONNMARK", "--restore-mark"},
	// 4. on *input* path ONLY, *change* mark from OUTGOING (UpstreamMark) to LOCAL_ROUTE (LocalRouteMark)
	// this achieves asymmetrical routing: packets with this mark are *outgoing* on egress path, and hijacked to *loopback* on ingress path
	{"iptables", "-t", "mangle", "-A", "ORB-PREROUTING", "-m", "mark", "--mark", agent.TlsProxyUpstreamMarkStr, "-j", "MARK", "--set-mark", agent.TlsProxyLocalRouteMarkStr},

	// prepare chains for TLS proxy
	{"ip6tables", "-t", "mangle", "-N", "ORB-PREROUTING"},
	{"ip6tables", "-t", "mangle", "-A", "PREROUTING", "-j", "ORB-PREROUTING"},
	{"ip6tables", "-t", "mangle", "-N", "ORB-OUTPUT"},
	{"ip6tables", "-t", "mangle", "-A", "OUTPUT", "-j", "ORB-OUTPUT"},

	// *** TLS proxy (v6)
	// complicated routing to make source IP spoofing work:
	// 1. outgoing socket has SO_MARK set to TlsProxyUpstreamMarkStr
	// 2. on OUTPUT path, save the OUTGOING mark to conntrack metadata for this 5-tuple
	{"ip6tables", "-t", "mangle", "-A", "ORB-OUTPUT", "-m", "mark", "--mark", agent.TlsProxyUpstreamMarkStr, "-j", "CONNMARK", "--save-mark"},
	// 3. on *input* PREROUTING path, restore the mark from conntrack metadata
	{"ip6tables", "-t", "mangle", "-A", "ORB-PREROUTING", "-m", "connmark", "--mark", agent.TlsProxyUpstreamMarkStr, "-j", "CONNMARK", "--restore-mark"},
	// 4. on *input* path ONLY, *change* mark from OUTGOING (UpstreamMark) to LOCAL_ROUTE (LocalRouteMark)
	// this achieves asymmetrical routing: packets with this mark are *outgoing* on egress path, and hijacked to *loopback* on ingress path
	{"ip6tables", "-t", "mangle", "-A", "ORB-PREROUTING", "-m", "mark", "--mark", agent.TlsProxyUpstreamMarkStr, "-j", "MARK", "--set-mark", agent.TlsProxyLocalRouteMarkStr},
}

// changes here:
//   - removed "health" from config (can't be overridden in custom config map)
//   - removed livenessProbe that uses /health. there's still a readinessProbe
//   - removed readinessProbe and increased memory limit to fix UDP conn refused under load
//   - readinessProbe can cause UDP conn refused (ICMP port unreachable), and since we only have 1 replica, directing traffic away is pointless (https://github.com/orbstack/orbstack/issues/763)
//   - added static NodeHosts to "coredns" ConfigMap (normally added by k3s)
//
//go:embed k8s/orb-coredns.yml
var k8sCorednsYaml []byte

type DockerDaemonFeatures struct {
	Buildkit bool `json:"buildkit"`
}

type DockerHooks struct {
	rootfs *securefs.FS
}

func newDockerHooks() (*DockerHooks, error) {
	rootfs, err := securefs.NewFS(conf.C().DockerRootfs)
	if err != nil {
		return nil, err
	}

	return &DockerHooks{
		rootfs: rootfs,
	}, nil
}

type SimplevisorConfig struct {
	InitCommands [][]string `json:"init_commands"`
	Services     [][]string `json:"services"`
}

type SimplevisorStatus struct {
	ExitStatuses []int `json:"exit_statuses"`
}

func (h *DockerHooks) createDataDirs() error {
	err := os.MkdirAll(conf.C().DockerDataDir, 0755)
	if err != nil {
		return err
	}

	// and k8s
	err = os.MkdirAll(conf.C().K8sDataDir, 0755)
	if err != nil {
		return err
	}
	kfs, err := securefs.NewFS(conf.C().K8sDataDir)
	if err != nil {
		return err
	}
	defer kfs.Close()

	// since we write to the data and use subdirs here, use securefs to prevent escape
	err = kfs.MkdirAll("/cni", 0755)
	if err != nil {
		return err
	}
	err = kfs.MkdirAll("/kubelet", 0755)
	if err != nil {
		return err
	}
	err = kfs.MkdirAll("/k3s", 0755)
	if err != nil {
		return err
	}
	err = kfs.MkdirAll("/etc-node", 0755)
	if err != nil {
		return err
	}

	// add customized coredns: healthcheck removed
	// /var/lib/rancher/k3s/server/manifests/coredns.yaml
	err = kfs.MkdirAll("/k3s/server/manifests", 0755)
	if err != nil {
		return err
	}
	err = kfs.WriteFile("/k3s/server/manifests/orb-coredns.yaml", []byte(k8sCorednsYaml), 0644)
	if err != nil {
		return err
	}

	return nil
}

// symlink everything from /mnt/mac/opt into /opt
// TODO: reverse proxy + path translation
func (h *DockerHooks) symlinkDirs() error {
	entries, err := os.ReadDir(mounts.Virtiofs + "/opt")
	if err != nil {
		return err
	}

	for _, entry := range entries {
		err = h.rootfs.Symlink(mounts.Virtiofs+"/opt/"+entry.Name(), "/opt/"+entry.Name())
		if err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
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
	// TODO: this could be a potential escape!
	cm.bind(conf.C().K8sDataDir+"/cni", "/var/lib/cni", "")
	cm.bind(conf.C().K8sDataDir+"/kubelet", "/var/lib/kubelet", "")
	cm.bind(conf.C().K8sDataDir+"/k3s", "/var/lib/rancher/k3s", "")
	// for password: /etc/rancher/node/password
	cm.bind(conf.C().K8sDataDir+"/etc-node", "/etc/rancher/node", "")
	// tmp (like dind)
	cm.set("lxc.mount.entry", "none run tmpfs rw,nosuid,nodev,mode=755 0 0")
	// writable for chmod/chown, + at path for Docker Desktop compat
	cm.bind(mounts.DockerSshAgentProxySocket, "/run/host-services/ssh-auth.sock", "")
	// match docker dind
	cm.set("lxc.mount.entry", "none dev/shm tmpfs rw,nosuid,nodev,noexec,relatime,size=65536k,create=dir 0 0")
	// alternate tmpfs because our /tmp is symlinked to /private/tmp
	cm.set("lxc.mount.entry", "none realtmp tmpfs rw,nosuid,nodev,nr_inodes=1048576,inode64,create=dir,optional,size=80% 0 0")
	// extra linked path: /System
	cm.bind(mounts.Virtiofs+"/System", "/System", "")

	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return "", fmt.Errorf("get user: %w", err)
	}

	// special case: make ~/.orbstack/run/docker.sock bind mount work (if people bind mount the docker context socket)
	// all mounts are optional so it's OK if this fails
	//cm.bind(mounts.DockerSocket, mounts.Virtiofs+hostUser.HomeDir+"/.orbstack/run/docker.sock", "")
	_ = hostUser

	// configure network statically
	cm.set("lxc.net.0.flags", "up")
	cm.set("lxc.net.0.ipv4.address", netconf.SconDockerIP4+"/24")
	cm.set("lxc.net.0.ipv4.gateway", netconf.SconGatewayIP4)
	// we put this in simplevisor init commands to bypass dad (sysctls are applied after ip addrs)
	/*
		cm.set("lxc.net.0.ipv6.address", netconf.SconDockerIP6+"/64")
		cm.set("lxc.net.0.ipv6.gateway", netconf.SconGatewayIP6)
	*/

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
	c.manager.k8sExposeServices = cfg.K8sExposeServices
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
	// each merge must use if-ok assertion to avoid panic on nil or unexpected type
	if newFeatures, ok := config["features"].(map[string]any); ok {
		for k, v := range newFeatures {
			baseFeatures[k] = v
		}
		config["features"] = baseFeatures
	}

	// merge builder map
	if newBuilder, ok := config["builder"].(map[string]any); ok {
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
	}

	// merge hosts list: make sure /var/run/docker.sock is always there if users add TCP hosts
	if newHosts, ok := config["hosts"].([]any); ok {
		if !slices.Contains(newHosts, "unix:///var/run/docker.sock") {
			newHosts = append(newHosts, "unix:///var/run/docker.sock")
		}
		config["hosts"] = newHosts
	}

	// iff IPv6 is enabled and user did not set a CIDR, set our default
	// otherwise keep it unset to avoid adding IPv6 to bridge IPAM
	if ipv6, ok := config["ipv6"].(bool); ok && ipv6 {
		if _, ok := config["fixed-cidr-v6"]; !ok {
			config["fixed-cidr-v6"] = "fd07:b51a:cc66:1::/64"
		}
	}

	// check for possible conflict between user-created bridge nets and default (bip)
	if bip, ok := config["bip"].(string); ok && bip != "" {
		conflictNet, err := dockerdb.CheckBipNetworkConflict(conf.C().DockerDataDir+"/network/files/local-kv.db", bip)
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, store.ErrKeyNotFound) {
			logrus.WithError(err).Error("failed to check docker bip conflict")
			conflictNet = nil
		}

		// to prevent infinite loop: if flag exists, delete it and bail out
		// we already tried once and it must've failed
		delErr := h.rootfs.Remove(agent.DockerNetMigrationFlag)
		if conflictNet != nil && errors.Is(delErr, os.ErrNotExist) {
			// migration needed
			logrus.WithField("bip", bip).WithField("conflictNet", conflictNet).Warn("docker bip conflict detected, migrating")

			// create flag file with orig config
			origConfig := config
			origConfigBytes, err := json.Marshal(&origConfig)
			if err != nil {
				return err
			}
			err = h.rootfs.WriteFile(agent.DockerNetMigrationFlag, []byte(origConfigBytes), 0644)
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
	err = h.rootfs.WriteFile("/etc/docker/daemon.json", configBytes, 0644)
	if err != nil {
		return err
	}

	// symlink ~/.docker/certs.d. host ensures this exists
	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	_ = h.rootfs.Remove("/etc/docker/certs.d")
	err = h.rootfs.Symlink(mounts.Virtiofs+hostUser.HomeDir+"/.docker/certs.d", "/etc/docker/certs.d")
	if err != nil {
		return fmt.Errorf("link certs: %w", err)
	}

	// write certs
	err = c.manager.getAndWriteCerts(h.rootfs, "/etc/ssl/certs")
	if err != nil {
		return fmt.Errorf("write certs: %w", err)
	}

	// get host timezone
	hostTimezone, err := c.manager.host.GetTimezone()
	if err != nil {
		return fmt.Errorf("get timezone: %w", err)
	}
	// create localtime symlink
	_ = h.rootfs.Remove("/etc/localtime")
	err = h.rootfs.Symlink("/usr/share/zoneinfo/"+hostTimezone, "/etc/localtime")
	if err != nil {
		logrus.WithError(err).Error("failed to symlink localtime")
	}

	svConfig := SimplevisorConfig{
		// must not be nil for rust
		// CAN'T MUTATE THIS GLOBAL! make a copy if needed
		InitCommands: dockerInitCommands,
		Services: [][]string{
			{"dockerd", "--host-gateway-ip=" + netconf.VnetHostNatIP4, "--userland-proxy-path", mounts.Pstub},
		},
	}
	// add TLS proxy iptables rules
	if c.manager.vmConfig.NetworkHttps {
		svConfig.InitCommands = append(svConfig.InitCommands, agent.DockerTlsInitCommands("-A")...)
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
			"--cluster-cidr", netconf.K8sClusterCIDR4 + "," + netconf.K8sClusterCIDR6,
			"--service-cidr", netconf.K8sServiceCIDR4 + "," + netconf.K8sServiceCIDR6,
			"--kube-controller-manager-arg", "node-cidr-mask-size-ipv4=" + netconf.K8sNodeCIDRMaskSize4,
			"--kube-controller-manager-arg", "node-cidr-mask-size-ipv6=" + netconf.K8sNodeCIDRMaskSize6,
			"--write-kubeconfig", "/run/kubeconfig.yml",
		}
		if conf.Debug() {
			k8sCmd = append(k8sCmd, "--enable-pprof")
		}
		svConfig.Services = append(svConfig.Services, k8sCmd)

		// add iptables rule to fix leaking 192.168.194.129:443 api server conns causing CrashLoopBackOff when services start and try to connect to api server
		// DROP in PREROUTING works because
		//   - prevents route from being resolved for the flow at conntrack level, so it tries again when pod retries SYN
		//   - KUBE rule is prepended so it takes priority once it's ready
		//   - can't use raw or prerouting because it would take prio over real k8s one
		// only do this for k8s to prevent issues if user has subnet conflict and only uses docker

		// remove old config symlink
		_ = h.rootfs.Remove("/etc/rancher/k3s/k3s.yaml")
	}

	// remove simplevisor exit status
	_ = h.rootfs.Remove("/.orb/svstatus.json")

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

	// symlink /opt dirs
	err = h.symlinkDirs()
	if err != nil {
		return fmt.Errorf("symlink dirs: %w", err)
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

	// check for simplevisor's dump
	exitStatus := 0
	var exitMsg string
	data, err := h.rootfs.ReadFile("/.orb/svstatus.json")
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		var svStatus SimplevisorStatus
		err = json.Unmarshal(data, &svStatus)
		if err != nil {
			return fmt.Errorf("parse status: %w", err)
		}

		// check for exit statuses
		for i, status := range svStatus.ExitStatuses {
			// -1 = did not exit before simplevisor stopped
			// also ignore signal exits - we requested those
			if status != 0 && status != -1 && status != (128+int(unix.SIGTERM)) && status != (128+int(unix.SIGINT)) && status != (128+int(unix.SIGKILL)) {
				logrus.WithField("status", status).WithField("service", i).Error("docker service exited with non-zero status")
				exitStatus = status
			}
		}
	}

	// read the log for non-zero exit status
	if exitStatus != 0 {
		exitMsg, err = c.readLogsLocked(types.LogConsole)
		if err != nil {
			logrus.WithError(err).Error("failed to read docker log")
		}
	}

	// slow, so use async if stopping (b/c we know it doesn't matter at that point)
	isAsync := c.manager.stopping
	err = c.manager.host.ClearDockerState(htypes.DockerExitInfo{
		Async: isAsync,
		ExitEvent: &uitypes.ExitEvent{
			Status:  exitStatus,
			Message: exitMsg,
		},
	})
	if err != nil {
		return fmt.Errorf("clear docker state: %w", err)
	}

	// clear blocked iptables forwards
	err = c.manager.net.ClearIptablesForwardBlocks()
	if err != nil {
		return fmt.Errorf("clear iptables: %w", err)
	}

	// unmount NFS images, volumes, containers
	err = c.manager.nfsForAll.UnmountAll("docker/")
	if err != nil {
		return fmt.Errorf("unmount nfs: %w", err)
	}

	// kill fpll servers
	err = c.manager.fpll.StopAll()
	if err != nil {
		return fmt.Errorf("stop fpll: %w", err)
	}

	// unmount everything from /nfs/containers
	err = c.manager.nfsContainers.UnmountAll("")
	if err != nil {
		return fmt.Errorf("unmount nfs containers: %w", err)
	}

	return nil
}
