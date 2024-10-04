package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lxc/go-lxc"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	startStopTimeout = 30 * time.Second
)

var (
	extraDevicePrefixes = []string{
		"loop",
		"nbd",
		"zram",
		"vdc",   // allow swapoff
		"ublkc", // ublk char device
		"ublkb", // ublk block device
	}

	// unfortunately we CANNOT exclude zram0 for non-isolated machines,
	// because it breaks swapoff
	// this risks leaking RAM from scon and VM host.
	extraDeviceExcludes = []string{}
)

func MatchesExtraDevice(name string) bool {
	for _, prefix := range extraDevicePrefixes {
		if strings.HasPrefix(name, prefix) && !slices.Contains(extraDeviceExcludes, name) {
			return true
		}
	}

	return false
}

func listDevExtra() ([]string, error) {
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return nil, err
	}

	devSrcs := make([]string, 0)
	for _, entry := range entries {
		if MatchesExtraDevice(entry.Name()) && entry.Name() != "loop-control" {
			devSrcs = append(devSrcs, path.Join("/dev", entry.Name()))
		}
	}

	return devSrcs, nil
}

// TODO lxc.hook.autodev or c.AddDeviceNode
func addInitDevice(c *Container, src string) error {
	// stat
	var stat unix.Stat_t
	err := unix.Stat(src, &stat)
	if err != nil {
		return err
	}

	var dtype string
	if stat.Mode&unix.S_IFMT == unix.S_IFBLK {
		// block device
		dtype = "b"
	} else {
		// char device
		dtype = "c"
	}

	// add to cgroups
	err = c.setLxcConfig("lxc.cgroup2.devices.allow", fmt.Sprintf("%s %d:%d rwm", dtype, unix.Major(stat.Rdev), unix.Minor(stat.Rdev)))
	if err != nil {
		return err
	}

	// add mount
	err = c.setLxcConfig("lxc.mount.entry", fmt.Sprintf("%s %s none bind,create=file,optional 0 0", src, strings.TrimPrefix(src, "/")))
	if err != nil {
		return err
	}

	return nil
}

func addInitBindMount(c *Container, src, dst, opts string) error {
	optsArr := strings.Split(opts, ",")

	isDir := false
	if !slices.Contains(optsArr, "nostat") {
		stat, err := os.Stat(src)
		if err != nil {
			return err
		}

		isDir = stat.IsDir()
	}

	createType := "file"
	bindType := "bind"
	if isDir {
		createType = "dir"
		bindType = "rbind"
	}

	optsArr = slices.DeleteFunc(optsArr, func(s string) bool {
		return s == "nostat"
	})

	extraOpts := strings.Join(optsArr, ",")

	err := c.setLxcConfig("lxc.mount.entry", fmt.Sprintf("%s %s none %s,create=%s,optional%s 0 0", src, strings.TrimPrefix(dst, "/"), bindType, createType, extraOpts))
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) setLxcConfig(key, value string) error {
	err := c.lxc.SetConfigItem(key, value)
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) initLxc() error {
	m := c.manager
	lc, err := lxc.NewContainer(c.ID, m.lxcDir)
	if err != nil {
		return err
	}
	// ref ok: while container is alive, ref is kept in manager maps
	runtime.SetFinalizer(lc, (*lxc.Container).Release)
	c.lxc = lc

	// set log file early in case we call get_state, stop, etc. before first start
	return c.configureLxcEarly()
}

func (c *Container) configureLxcEarly() error {
	lc := c.lxc

	// logging
	lc.ClearConfig()
	err := lc.SetLogFile(c.logPath())
	if err != nil {
		return err
	}
	if conf.Debug() {
		lc.SetVerbosity(lxc.Verbose)
		err = lc.SetLogLevel(lxc.TRACE)
		if err != nil {
			return err
		}
	} else {
		lc.SetVerbosity(lxc.Quiet)
		err = lc.SetLogLevel(lxc.NOTICE)
		if err != nil {
			return err
		}
	}

	return nil
}

// most of this is done lazily on first start:
// makes scon start faster, and some configs require a valid rootfs
func (c *Container) configureLxc() error {
	m := c.manager

	// configs
	err := os.MkdirAll(c.rootfsDir, 0755)
	if err != nil {
		return err
	}
	rootfs, err := filepath.EvalSymlinks(c.rootfsDir)
	if err != nil {
		return err
	}
	mac := deriveMacAddress(c.ID)

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	hostUser, err := m.host.GetUser()
	if err != nil {
		return err
	}

	sshAgentSocks, err := c.manager.host.GetSSHAgentSockets()
	if err != nil {
		return err
	}

	// /sys/block only includes disks. /sys/class/block includes partitions too
	blockDevs, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return err
	}

	// set configs!
	err = func() (err error) {
		defer func() {
			if err2 := recover(); err2 != nil {
				err = fmt.Errorf("configure LXC: %v", err2)
			}
		}()

		set := func(key, value string) {
			if err := c.setLxcConfig(key, value); err != nil {
				// hide config values in release
				if conf.Debug() {
					panic(fmt.Errorf("set %s=%s: %w", key, value, err))
				} else {
					panic(fmt.Errorf("set %s: %w", key, err))
				}
			}
		}

		addDevOptional := func(node string) {
			if err := addInitDevice(c, node); err != nil {
				// this is only warning - devices are optional
				logrus.WithError(err).WithField("node", node).Debug("add device failed")
			}
		}
		bind := func(src, dst, opts string) {
			if err := addInitBindMount(c, src, dst, opts); err != nil {
				panic(fmt.Errorf("bind %s -> %s: %w", src, dst, err))
			}
		}

		/*
		* from LXD
		 */
		set("lxc.pty.max", "1024")
		set("lxc.tty.max", "0")
		set("lxc.autodev", "1") // populate /dev

		// console
		set("lxc.console.logfile", c.logPath()+"-console")
		set("lxc.console.buffer.size", "auto")
		set("lxc.console.size", "auto")

		// make sure to block krpc if denylist
		set("lxc.cgroup2.devices.deny", "a")
		set("lxc.cgroup2.devices.allow", "b *:* m") // mknod block
		if !c.config.Isolated {
			set("lxc.cgroup2.devices.allow", "b 7:* rwm")  // dev/loop*
			set("lxc.cgroup2.devices.allow", "b 43:* rwm") // dev/nbd*
		}
		set("lxc.cgroup2.devices.allow", "c *:* m")     // mknod char
		set("lxc.cgroup2.devices.allow", "c 136:* rwm") // dev/pts/*
		set("lxc.cgroup2.devices.allow", "c 1:3 rwm")   // dev/null
		set("lxc.cgroup2.devices.allow", "c 1:5 rwm")   // dev/zero
		set("lxc.cgroup2.devices.allow", "c 1:7 rwm")   // dev/full
		set("lxc.cgroup2.devices.allow", "c 1:8 rwm")   // dev/random
		set("lxc.cgroup2.devices.allow", "c 1:9 rwm")   // dev/urandom
		set("lxc.cgroup2.devices.allow", "c 5:0 rwm")   // dev/tty
		set("lxc.cgroup2.devices.allow", "c 5:1 rwm")   // dev/console
		set("lxc.cgroup2.devices.allow", "c 5:2 rwm")   // dev/ptmx

		// Devices
		addDevOptional("/dev/fuse")
		addDevOptional("/dev/net/tun")
		addDevOptional("/dev/ppp")
		addDevOptional("/dev/vhost-net")
		addDevOptional("/dev/kvm")
		if !c.config.Isolated {
			addDevOptional("/dev/kmsg")
			addDevOptional("/dev/loop-control")
			addDevOptional("/dev/ublk-control")
			addDevOptional("/dev/autofs") // TODO security
			addDevOptional("/dev/userfaultfd")
			addDevOptional("/dev/btrfs-control")
			addDevOptional("/dev/binder")
			addDevOptional("/dev/vndbinder")
			addDevOptional("/dev/hwbinder")
			addDevOptional("/dev/vhost-vsock")

			// add /dev/vdb1 to make k3s happy
			// it only needs stat, but no harm in letting people access this if they need to for whatever reason
			addDevOptional(conf.C().DataFsDevice)
		} else {
			// isolated should still be able to stat, for k3s. just deny it via devices cgroup
			bind(conf.C().DataFsDevice, conf.C().DataFsDevice, "")
		}

		// other block devices should be added to make some apps happy
		// just don't allow in devices cgroup, so there's no real harm
		for _, entry := range blockDevs {
			bind("/dev/"+entry.Name(), "/dev/"+entry.Name(), "")
		}

		// Default mounts
		set("lxc.mount.auto", "proc:rw sys:mixed cgroup:rw:force")
		set("lxc.mount.entry", "mqueue dev/mqueue mqueue rw,relatime,create=dir,optional 0 0")
		// don't let people mess with binfmt_misc
		//set("lxc.mount.entry", "/proc/sys/fs/binfmt_misc proc/sys/fs/binfmt_misc none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "/sys/fs/fuse/connections sys/fs/fuse/connections none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "/sys/kernel/security sys/kernel/security none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "bpf sys/fs/bpf bpf rw,nosuid,nodev,noexec,relatime,mode=700,optional 0 0")
		bind("/sys/kernel/tracing", "/sys/kernel/tracing", "")
		if !c.config.Isolated {
			// this is recursive (rbind) so no need for /sys/kernel/debug/tracing
			bind("/sys/kernel/debug", "/sys/kernel/debug", "")
		}

		// kernel modules used to be a symlink, but bind mount plays nicer with docker (in machines too):
		// people mount it with "-v /lib/modules" so
		// however, /lib is often a symlink and LXC doesn't allow symlinks in mount dest paths, so we have to resolve it here
		// match LXC behavior: auto-create /lib/modules
		_ = securefs.MkdirAll(c.rootfsDir, "/lib/modules", 0755)
		libModulesPath, err := securefs.ResolvePath(c.rootfsDir, "/lib/modules")
		if err != nil {
			logrus.WithError(err).WithField("machine", c.Name).Error("failed to resolve /lib/modules")
		} else {
			guestPath := strings.TrimPrefix(libModulesPath, c.rootfsDir)
			kernelVersionPath := guestPath + "/" + c.manager.kernelVersion
			bind(conf.C().GuestMountSrc+"/lib/modules/current", kernelVersionPath, "ro")
		}

		// nesting (proc not needed because it's rw)
		// this is in .lxc not .orbstack because of lxc systemd-generator's conditions
		// see /etc/systemd/system-generators/lxc - we effectively have security.nesting on
		// also, we still need uncovered mounts for nesting, even if /proc is rw
		// otherwise nixpkgs unpacking fails: error: mounting /proc: Operation not permitted
		set("lxc.mount.entry", "proc dev/.lxc/proc proc create=dir,optional 0 0")
		set("lxc.mount.entry", "sys dev/.lxc/sys sysfs create=dir,optional 0 0")

		// premount a bigger tmpfs
		// docker uses a /tmp -> /private/tmp symlink instead
		// TODO: why was this added in the first place?
		if c.Image.Distro != images.ImageDocker {
			set("lxc.mount.entry", "tmpfs tmp tmpfs rw,nosuid,nodev,nr_inodes=1048576,inode64,create=dir,optional,size=80% 0 0")
		}

		// other
		set("lxc.apparmor.profile", "unconfined")
		set("lxc.arch", "linux64")

		// shutdown fixes
		if c.Image.Distro == images.ImageVoid {
			// void: runit - SIGCONT
			set("lxc.signal.halt", "SIGCONT")
		} else if c.Image.Distro == images.ImageAlpine {
			// alpine: busybox/OpenRC - SIGUSR1
			set("lxc.signal.halt", "SIGUSR1")
		} else if c.ID == ContainerIDDocker {
			// docker: tini (docker-init) - SIGTERM
			set("lxc.signal.halt", "SIGTERM")
		}

		/*
		 * custom
		 */
		// seccomp
		set("lxc.seccomp.allow_nesting", "1")
		// select policy: emulated? isolated?
		var policyType SeccompPolicyType
		isEmulated := c.Image.Arch != runtime.GOARCH
		if isEmulated {
			if c.config.Isolated {
				policyType = SeccompPolicyEmulatedIsolated
			} else {
				policyType = SeccompPolicyEmulated
			}
		} else {
			if c.config.Isolated {
				policyType = SeccompPolicyIsolated
			} else {
				policyType = SeccompPolicyNone
			}
		}
		if policyType != SeccompPolicyNone {
			set("lxc.seccomp.profile", m.seccompPolicyPaths[policyType])
		}

		if c.config.Isolated {
			// identity user namespace mapping to get capability restrictions without changing FS UID/GID
			// this also removes the need to drop CAP_SYS_RAWIO/CAP_SYS_TIME
			// math.MaxUint32
			set("lxc.idmap", "u 0 0 4294967295")
			set("lxc.idmap", "g 0 0 4294967295")
		}

		// faster ipv6 config
		set("lxc.sysctl.net.ipv6.conf.eth0.accept_dad", "0")

		// network
		set("lxc.net.0.type", "veth")
		set("lxc.net.0.veth.mode", "bridge")
		set("lxc.net.0.link", ifBridge)
		set("lxc.net.0.mtu", strconv.Itoa(m.net.mtu))
		set("lxc.net.0.hwaddr", mac)
		set("lxc.net.0.flags", "up")
		if c.config.Isolated {
			// set interface group for isolated containers
			set("lxc.net.0.script.up", fmt.Sprintf("%s %s %s %s", exePath, cmdLxcHook, lxcHookNetIfUp, c.ID))
		}

		// rlimit
		// lower soft limit to avoid slowing down programs that iterate through and close full fd range, but not so low (1024)
		// high hard limit so users can set what they want
		// ref: https://github.com/containerd/containerd/pull/7566
		// oceanbase db wants >=20000
		set("lxc.prlimit.nofile", "20480:1048576")

		// reset oom_score_adj
		set("lxc.proc.oom_score_adj", "0")

		// bind mounts
		bind(mounts.Opt, mounts.Opt, "ro")

		// isolated containers don't get bind mounts
		if !c.config.Isolated {
			// guest service sockets only for non-isolated containers
			bind("/data/guest-state", mounts.Data, "ro")
			bind(mounts.HostRun, mounts.Run, "ro")

			bind(conf.C().HostMountSrc, "/mnt/mac", "")
			// we're doing this in kernel now, to avoid showing up in `df`
			//bind(conf.C().FakeSrc+"/sysctl/kernel.panic", "/proc/sys/kernel/panic", "ro")

			// binds for mac linked paths
			// symlinks cause problems with vs code, git, etc. so we bind them
			for _, p := range mounts.LinkedPaths {
				bind("/mnt/mac"+p, p, "")
			}

			// binds for ssh agent sockets (fixes docker $SSH_AUTH_SOCK forward)
			// anything operation (mount, stat, access) on the /private socket through virtiofs returns EOPNOTSUPP
			// so we bind the dir to a tmpfs
			if sshAgentSocks.Env != "" && strings.HasPrefix(sshAgentSocks.Env, "/private/tmp/com.apple.launchd.") {
				bind(mounts.LaunchdSshAgentListeners, path.Dir(sshAgentSocks.Env), "ro")
			}

			// bind NFS root at /mnt/machines for access
			// must be rslave for agent's ~/OrbStack bind to work later
			bind(nfsDirForMachines+"/ro", "/mnt/machines", "ro,rslave")
			// also bind it to ~/OrbStack so paths work correctly
			// with krun/rsvm VMM, this is OK because we replace the dir with /var/empty, and inode never changes
			// will break with VZF's virtiofs server!
			bind(nfsDirForMachines+"/ro", hostUser.HomeDir+"/"+mounts.NfsDirName, "ro,rslave")
		}

		// emulated machines get a fake /proc/cpuinfo
		if isEmulated {
			bind("/run/rosetta-cpuinfo", "/proc/cpuinfo", "ro")
		}

		// allow hook to override this
		set("lxc.uts.name", c.Name)

		// extra cgroup level so container can't remove limits
		// works w/ kernel commit: "cgroup: allow root and its grandchildren to mix children and controllers"
		set("lxc.cgroup.dir.monitor", "scon.monitor."+c.ID)
		set("lxc.cgroup.dir.container", "scon.container."+c.ID)
		set("lxc.cgroup.dir.container.inner", bpf.ChildCgroupName)

		// container hooks, before rootfs is set
		if c.hooks != nil {
			newRootfs, err := c.hooks.Config(c, containerConfigMethods{
				set:  set,
				bind: bind,
			})
			if err != nil {
				panic(err)
			}
			if newRootfs != "" {
				rootfs = newRootfs
			}
		}

		// container
		set("lxc.rootfs.path", "dir:"+rootfs)

		// hooks
		set("lxc.hook.version", "1")
		set("lxc.hook.post-stop", fmt.Sprintf("%s %s %s %s", exePath, cmdLxcHook, lxcHookPostStop, c.ID))

		return nil
	}()
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) logPath() string {
	return c.manager.subdir("logs") + "/" + c.ID + ".log"
}

func mergeBuiltinContainers(records []*types.ContainerRecord, builtins []*types.ContainerRecord) []*types.ContainerRecord {
	// find existing builtins
	for _, builtin := range builtins {
		found := false
		for _, record := range records {
			if record.ID == builtin.ID {
				// if found, update the builtin state, but otherwise keep ours
				// this makes the start/stop state persistent while keeping the rest built in
				logrus.WithField("container", record.Name).Debug("found persisted builtin container")
				builtin.State = record.State

				// then replace the entire record
				*record = *builtin

				found = true
				break
			}
		}

		// if not found, add it
		if !found {
			logrus.WithField("container", builtin.Name).Debug("adding builtin container")
			records = append(records, builtin)
		}
	}

	// filter: remove any builtins that are not in the list
	// for forward compatibility
	var filtered []*types.ContainerRecord
	for _, record := range records {
		if !record.Builtin {
			filtered = append(filtered, record)
		}

		found := false
		for _, builtin := range builtins {
			if record.ID == builtin.ID {
				found = true
				break
			}
		}

		if found {
			filtered = append(filtered, record)
		}
	}

	return filtered
}

func (m *ConManager) restoreContainers() ([]*Container, error) {
	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	records, err := m.db.GetContainers()
	if err != nil {
		return nil, err
	}

	// add or update builtins
	dockerCopy := dockerContainerRecord
	records = mergeBuiltinContainers(records, []*types.ContainerRecord{&dockerCopy})

	var pendingStarts []*Container
	for _, record := range records {
		c, shouldStart, err := m.restoreOneLocked(record, false /*isNew*/)
		if err != nil {
			logrus.WithError(err).WithField("container", record.Name).Error("failed to restore container")
			continue
		}

		logrus.WithField("container", c.Name).WithField("record", record).Debug("restored container")
		if shouldStart {
			pendingStarts = append(pendingStarts, c)
		}
	}

	logrus.WithField("pending", pendingStarts).Debug("will start containers")
	return pendingStarts, nil
}

func (m *ConManager) insertContainerLocked(c *Container) error {
	if _, ok := m.containersByID[c.ID]; ok {
		return fmt.Errorf("machine '%q' already exists", c.ID)
	}
	if _, ok := m.containersByName[c.Name]; ok {
		return fmt.Errorf("machine '%q' already exists", c.Name)
	}
	for _, alias := range c.Aliases {
		if _, ok := m.containersByName[alias]; ok {
			return fmt.Errorf("machine '%q' already exists", alias)
		}
	}

	m.containersByID[c.ID] = c
	m.containersByName[c.Name] = c
	for _, alias := range c.Aliases {
		m.containersByName[alias] = c
	}
	return nil
}

func (m *ConManager) restoreOneLocked(record *types.ContainerRecord, isNew bool) (*Container, bool, error) {
	// fix/upgrade container record:
	// isolated = false
	// empty (old/unset) default username -> set to default host user
	//   * this case is also used for new machine creation with default username
	if record.Config.DefaultUsername == "" {
		logrus.WithField("container", record.Name).Debug("setting username to default")
		defaultUser, err := m.defaultUser()
		if err != nil {
			return nil, false, fmt.Errorf("get default user: %w", err)
		}
		record.Config.DefaultUsername = defaultUser
	}

	c, err := m.newContainerLocked(record)
	if err != nil {
		return nil, false, err
	}

	// restore creating state early to avoid race where user can start it
	if record.State == types.ContainerStateCreating {
		c.setState(types.ContainerStateCreating)
	}

	err = m.insertContainerLocked(c)
	if err != nil {
		return nil, false, fmt.Errorf("duplicate in database: %w", err)
	}

	// delete if interrupted during creation
	if (!isNew && record.State == types.ContainerStateCreating) || record.State == types.ContainerStateDeleting {
		go func() {
			err := c.Delete()
			if err != nil {
				logrus.WithError(err).WithField("container", c.Name).Error("failed to delete container")
			}
		}()
	}

	// if not creating or deleting, run container restore hooks
	// this requires rootfs to exist, which may not be the case until creation is done
	if record.State != types.ContainerStateCreating && record.State != types.ContainerStateDeleting {
		go func() {
			err := m.onRestoreContainer(c)
			if err != nil {
				logrus.WithError(err).WithField("container", c.Name).Error("container restore hook failed")
			}
		}()
	}

	m.uiEventDebounce.Call()

	shouldStart := record.State == types.ContainerStateRunning
	return c, shouldStart, nil
}

func (c *Container) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.startLocked(false /* isInternal */)
}

func (c *Container) startLxcLocked() error {
	// no new fds in between
	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()

	return c.lxc.Start()
}

func (c *Container) startLocked(isInternal bool) (retErr error) {
	if c.runningLocked() {
		return nil
	}

	if c.manager.stopping {
		return ErrStopping
	}

	if !c.lxcConfigured {
		err := c.configureLxc()
		if err != nil {
			return fmt.Errorf("configure LXC: %w", err)
		}
		c.lxcConfigured = true
	}

	oldState, err := c.transitionStateInternalLocked(types.ContainerStateStarting, isInternal)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			c.revertStateLocked(oldState)
		}
	}()

	logrus.WithField("container", c.Name).Info("starting container")

	// clean logs (truncate - otherwise logs are gone because they're opened when creating lxc container)
	err = os.WriteFile(c.logPath(), nil, 0644)
	if err != nil {
		return fmt.Errorf("truncate log: %w", err)
	}
	_ = os.Remove(c.logPath() + "-console")

	// randomize cgroup paths in case an old one was left behind
	// usually only happens in dev when scon is killed, so conflict risk is very low in prod
	// base36 to minimize length
	// needed because lxc doesn't iterate and append index if we set cgroups explicitly (which we need for inner child cgroup for security)
	randSuffix := c.ID + "." + strconv.FormatUint(uint64(rand.Uint32()), 36)
	err = c.setLxcConfig("lxc.cgroup.dir.monitor", "scon.monitor."+randSuffix)
	if err != nil {
		return fmt.Errorf("set cgroup: %w", err)
	}
	err = c.setLxcConfig("lxc.cgroup.dir.container", "scon.container."+randSuffix)
	if err != nil {
		return fmt.Errorf("set cgroup: %w", err)
	}

	// hook
	if c.hooks != nil {
		err := c.hooks.PreStart(c)
		if err != nil {
			return err
		}
	}

	err = c.startLxcLocked()
	if err != nil {
		return fmt.Errorf("start '%s': %w", c.Name, err)
	}

	// for some reason our process (in addition to monitor) gets moved into lxc monitor cgroup,
	// so move it back
	// TODO: file an issue. it's not caused by parent-cgroup kernel hack
	err = os.WriteFile("/sys/fs/cgroup/cgroup.procs", []byte(strconv.Itoa(os.Getpid())), 0644)
	if err != nil {
		return fmt.Errorf("move process to root cgroup: %w", err)
	}

	if !c.lxc.Wait(lxc.RUNNING, startStopTimeout) {
		return fmt.Errorf("machine did not start: %s - %v", c.Name, c.lxc.State())
	}
	// after this point: if it failed, attempt to stop lxc
	defer func() {
		if retErr != nil {
			_ = c.lxc.Stop()
		}
	}()

	// TODO: what if it crashed?
	initPid := c.lxc.InitPid()
	if initPid == -1 {
		return fmt.Errorf("machine '%s' failed to start: init crashed", c.Name)
	}
	c.initPid = initPid
	initPidFile, err := c.lxc.InitPidFd()
	if err != nil {
		return fmt.Errorf("machine '%s' failed to start: init crashed (%w)", c.Name, err)
	}
	c.initPidFile = initPidFile

	err = c.startAgentLocked()
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("failed to start agent")
	}

	// attach loop devices
	extraDevSrcs, err := listDevExtra()
	if err != nil {
		return err
	}
	for _, src := range extraDevSrcs {
		err := c.addDeviceNode(src, src)
		if err != nil {
			return err
		}
	}

	// add to mDNS registry
	c.manager.net.mdnsRegistry.AddMachine(c)

	err = c.onStartLocked()
	if err != nil {
		return err
	}

	go func() {
		err := c.manager.net.RefreshFlowtable()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to refresh FT")
		}
	}()

	logrus.WithField("container", c.Name).Info("container started")

	return nil
}

func (c *Container) onStartLocked() error {
	_, err := c.transitionStateLocked(types.ContainerStateRunning)
	if err != nil {
		return err
	}

	// hook
	if c.hooks != nil {
		err := c.hooks.PostStart(c)
		if err != nil {
			return err
		}
	}

	return nil
}

func findAgentExe() (string, error) {
	curExe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return path.Join(path.Dir(curExe), "scon-agent"), nil
}

func padAgentCmd(cmd string) string {
	// target len = at least len(agent.ProcessName)
	targetLen := len(agent.ProcessName) + agent.ProcessNameExtraPad
	if len(cmd) < targetLen {
		// prepend slashes
		cmd = strings.Repeat("/", targetLen-len(cmd)) + cmd
	}
	return cmd
}

func (c *Container) startAgentLocked() error {
	logrus.WithField("container", c.Name).Debug("starting agent")

	// make agent fds
	rpcFile, fdxFile, rpcConn, fdxConn, err := agent.MakeAgentFds()
	if err != nil {
		return err
	}
	// close our side of the file fds after start
	defer rpcFile.Close()
	defer fdxFile.Close()

	// add some more fds
	exeFd := int(c.manager.agentExe.Fd())
	args := []string{padAgentCmd("/proc/self/fd/" + strconv.Itoa(exeFd)), "fork"}
	if c.ID == ContainerIDDocker {
		args = append(args, "-docker")
		// TODO: fix race if TLS setting changed between container start (initCommands) and agent start
		if c.manager.vmConfig.NetworkHttps {
			args = append(args, "-tls")
		}
	}
	if c.ID == ContainerIDK8s && c.manager.k8sEnabled {
		args = append(args, "-k8s")
	}

	cmd := &LxcCommand{
		CombinedArgs: args,
		Dir:          "/",
		Env:          []string{},
		Stdin:        rpcFile,
		Stdout:       fdxFile,
		Stderr:       os.Stderr,

		// all except
		//   pid - to hide agent
		//   user - because we don't use it
		restrictNamespaces: ^0,
		extraFd:            exeFd,
	}
	err = cmd.Start(c)
	if err != nil {
		return err
	}
	// probably not needed
	runtime.KeepAlive(c.manager.agentExe)

	// Stop() hangs without this
	go cmd.Process.Wait()

	// agent client
	client := agent.NewClient(cmd.Process, rpcConn, fdxConn)
	oldClient := c.agent.Swap(client)
	// close old client if any
	if oldClient != nil {
		oldClient.Close()
	}

	// async tasks, so we can release the lock
	// can block if agent is broken
	// also, we'll close our side of the remote fd so RPC will return ECONNRESET
	go func() {
		err := c.postStartAsync(client)
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to init agent")
		}
	}()

	return nil
}

func findCgroup(pid int) (string, error) {
	cgList, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", fmt.Errorf("read cgroup: %w", err)
	}
	for _, line := range strings.Split(string(cgList), "\n") {
		// only works for cgroup v2 (no controllers list)
		if strings.HasPrefix(line, "0::") {
			return line[3:], nil
		}
	}

	return "", fmt.Errorf("no cgroup found")
}

func (c *Container) attachBpf(initPid int) error {
	// netns cookie
	netnsCookie, err := withContainerNetns(c, func() (uint64, error) {
		return sysnet.GetNetnsCookie()
	})
	if err != nil {
		return fmt.Errorf("get netns cookie: %w", err)
	}

	// find cgroup
	cgGroup, err := findCgroup(initPid)
	if err != nil {
		return fmt.Errorf("find cgroup: %w", err)
	}
	// only take the first part ("scon.container.") in case systemd created init.scope
	cgPath := "/sys/fs/cgroup/" + strings.Split(cgGroup, "/")[1]

	bpfMgr, err := bpf.NewContainerBpfManager(cgPath, netnsCookie)
	if err != nil {
		return fmt.Errorf("new bpf: %w", err)
	}
	c.bpf = bpfMgr

	// attach pmon
	includeNft := c.ID == ContainerIDK8s
	pmonReader, err := bpfMgr.AttachPmon(includeNft)
	if err != nil {
		return fmt.Errorf("attach bpf pmon: %w", err)
	}

	go runOne("pmon monitor for "+c.Name, func() error {
		return bpf.MonitorPmon(pmonReader, func(ev bpf.PmonEvent) error {
			c.triggerListenersUpdate(ev.DirtyFlags)
			return nil
		})
	})

	// attach lfwd for docker
	if c.ID == ContainerIDDocker {
		err := bpfMgr.AttachLfwd()
		if err != nil {
			return fmt.Errorf("attach bpf lfwd: %w", err)
		}
	}

	return nil
}

func (c *Container) initNetPostStart() error {
	// we set cleanup fields here
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.config.Isolated {
		// isolated containers don't have auto listener or bpf reverse localhost forward
		return nil
	}

	// need pid, not pidfd, to open /proc/PID/cgroup
	initPid := c.initPid
	if initPid == 0 {
		return fmt.Errorf("no init pid")
	}

	// attach bpf localhost reverse forward for Docker
	err := c.attachBpf(initPid)
	if err != nil {
		return fmt.Errorf("attach bpf: %w", err)
	}
	logrus.WithField("container", c.Name).Debug("attached bpf")

	return nil
}

func (c *Container) postStartAsync(a *agent.Client) error {
	// does not really fit in postStartAsync, but not critical so we do it here
	// compiling bpf programs is a little slow (3 ms)
	err := c.initNetPostStart()
	if err != nil {
		return fmt.Errorf("init net: %w", err)
	}

	// kick listener update in case we missed any before agent start
	c.triggerListenersUpdate(bpf.LtypeAll)

	// get agent's pidfd
	err = a.MonitorAgentPidFd()
	if err != nil {
		return fmt.Errorf("get agent pidfd: %w", err)
	}

	// postStartAsync runs asynchronously, so we can reuse this goroutine

	// wait for agent to stop
	_ = a.WaitStop()

	// if agent crashed and container is still running, stop the container
	c.mu.RLock()
	defer c.mu.RUnlock()

	err = c.useAgentLocked(func(currentAgent *agent.Client) error {
		// if this is the current agent, then container is still running and has a reference to this agent
		if currentAgent == a {
			logrus.WithField("container", c.Name).Error("agent crashed, stopping container")
			_, err := c.stopLocked(StopOptions{
				KillProcesses:     false,
				ManagerIsStopping: false,
			})
			return err
		} else {
			return nil
		}
	})
	// if container isn't running, we're fine
	// otherwise something's wrong
	if err != nil && !errors.Is(err, ErrMachineNotRunning) {
		logrus.WithError(err).WithField("container", c.Name).Error("failed to stop container after agent crash")
	}

	return nil
}
