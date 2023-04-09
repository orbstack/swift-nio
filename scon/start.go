package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/kdrag0n/macvirt/scon/util/sysnet"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

const (
	startTimeout = 15 * time.Second
)

var (
	extraDevicePrefixes = []string{
		"loop",
		"nbd",
		"zram",
	}

	extraDeviceExcludes = []string{
		"zram0",
	}
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
	extraOpts := ""
	if opts != "" {
		extraOpts = "," + opts
	}

	stat, err := os.Stat(src)
	if err != nil {
		return err
	}

	createType := "file"
	bindType := "bind"
	if stat.IsDir() {
		createType = "dir"
		bindType = "rbind"
	}

	err = c.setLxcConfig("lxc.mount.entry", fmt.Sprintf("%s %s none %s,create=%s,optional%s 0 0", src, strings.TrimPrefix(dst, "/"), bindType, createType, extraOpts))
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

	return c.configureLxc()
}

func (c *Container) configureLxc() error {
	m := c.manager
	lc := c.lxc

	// logging
	logPath := m.subdir("logs") + "/" + c.ID + ".log"
	lc.ClearConfig()
	lc.SetLogFile(logPath)
	if conf.Debug() {
		lc.SetVerbosity(lxc.Verbose)
		lc.SetLogLevel(lxc.TRACE)
	} else {
		lc.SetVerbosity(lxc.Quiet)
		lc.SetLogLevel(lxc.NOTICE)
	}

	// configs
	rootfs := path.Join(c.dir, "rootfs")
	err := os.MkdirAll(rootfs, 0755)
	if err != nil {
		return err
	}
	rootfs, err = filepath.EvalSymlinks(rootfs)
	if err != nil {
		return err
	}
	cookieStr, cookieU64, err := makeSeccompCookie()
	if err != nil {
		return err
	}
	mac := deriveMacAddress(c.ID)

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	sshAgentSocks, err := c.manager.host.GetSSHAgentSockets()
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
				logrus.WithError(err).WithField("node", node).Warn("add device failed")
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
		// limiting caps breaks privileged nested docker 20.10.x containers (fixed in 23.0)
		// because of dummy debugfs, we limit CAP_SYS_RAWIO so the systemd service condition fails
		// otherwise it tries to mount if ConditionPathExists=/sys/kernel/debug and ConditionCapability=CAP_SYS_RAWIO
		//set("lxc.cap.drop", "sys_rawio")
		set("lxc.autodev", "1") // populate /dev

		// console
		set("lxc.console.logfile", logPath+"-console")
		set("lxc.console.buffer.size", "auto")
		set("lxc.console.size", "auto")

		set("lxc.cgroup2.devices.deny", "a")
		set("lxc.cgroup2.devices.allow", "b *:* m")     // mknod block
		set("lxc.cgroup2.devices.allow", "b 7:* rwm")   // dev/loop*
		set("lxc.cgroup2.devices.allow", "b 43:* rwm")  // dev/nbd*
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
		addDevOptional("/dev/kmsg")
		addDevOptional("/dev/loop-control")
		addDevOptional("/dev/autofs") // TODO security
		addDevOptional("/dev/userfaultfd")
		addDevOptional("/dev/btrfs-control")
		addDevOptional("/dev/binder")
		addDevOptional("/dev/vndbinder")
		addDevOptional("/dev/hwbinder")

		// add /dev/vdb1 to make k3s happy - it just wants to stat
		// but keep it blocked in devices cgroup
		bind("/dev/vdb1", "/dev/vdb1", "")

		// Default mounts
		set("lxc.mount.auto", "proc:rw sys:mixed cgroup:rw:force")
		set("lxc.mount.entry", "mqueue dev/mqueue mqueue rw,relatime,create=dir,optional 0 0")
		// don't let people mess with binfmt_misc
		//set("lxc.mount.entry", "/proc/sys/fs/binfmt_misc proc/sys/fs/binfmt_misc none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "/sys/fs/fuse/connections sys/fs/fuse/connections none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "/sys/kernel/security sys/kernel/security none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "bpf sys/fs/bpf bpf rw,nosuid,nodev,noexec,relatime,mode=700,optional 0 0")
		bind("/sys/kernel/tracing", "/sys/kernel/tracing", "")
		// this is recursive (rbind) so no need for /sys/kernel/debug/tracing
		bind("/sys/kernel/debug", "/sys/kernel/debug", "")

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
		}

		/*
		 * custom
		 */
		// seccomp
		set("lxc.seccomp.allow_nesting", "1")
		set("lxc.seccomp.notify.proxy", "unix:"+m.seccompProxySock)
		set("lxc.seccomp.profile", m.seccompPolicyPath)
		set("lxc.seccomp.notify.cookie", cookieStr)

		// network
		set("lxc.net.0.type", "veth")
		// TODO try router
		set("lxc.net.0.veth.mode", "bridge")
		set("lxc.net.0.link", ifBridge)
		set("lxc.net.0.mtu", strconv.Itoa(m.net.mtu))
		set("lxc.net.0.hwaddr", mac)
		// faster ipv6 config
		set("lxc.sysctl.net.ipv6.conf.eth0.accept_dad", "0")

		// bind mounts
		config := conf.C()
		bind(config.GuestMountSrc, "/opt/orbstack-guest", "ro")
		bind(config.HostMountSrc, "/mnt/mac", "")
		// we're doing this in kernel now, to avoid showing up in `df`
		//bind(config.FakeSrc+"/sysctl/kernel.panic", "/proc/sys/kernel/panic", "ro")

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
		// must be rshared for agent's ~/OrbStack bind to work later
		bind(config.NfsRootRO, "/mnt/machines", "ro,rshared")
		// we also bind it to ~/OrbStack later so paths work correctly
		// but must do it AFTER macOS host mounts NFS on the path
		// otherwise, kernel sees that inode has changed and unmounts everything
		// https://github.com/torvalds/linux/commit/8ed936b5671bfb33d89bc60bdcc7cf0470ba52fe

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
		set("lxc.uts.name", c.Name)

		// hooks
		set("lxc.hook.version", "1")
		set("lxc.hook.post-stop", fmt.Sprintf("/opt/orb/daemonize %s %s %s %s", exePath, cmdLxcHook, lxcHookPostStop, c.ID))

		return nil
	}()
	if err != nil {
		return err
	}

	c.seccompCookie = cookieU64
	c.rootfsDir = rootfs

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
		c, shouldStart, err := m.restoreOneLocked(record, true)
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

func (m *ConManager) insertContainerLocked(c *Container) {
	m.containersByID[c.ID] = c
	m.containersByName[c.Name] = c
}

func (m *ConManager) restoreOneLocked(record *types.ContainerRecord, canOverwrite bool) (*Container, bool, error) {
	c, err := m.newContainerLocked(record)
	if err != nil {
		return nil, false, err
	}

	// important to restore creating state
	if record.State == types.ContainerStateCreating {
		c.state = types.ContainerStateCreating
	}

	if oldC, ok := m.getByNameLocked(c.Name); ok {
		if canOverwrite {
			// ok, but warn
			// too dangerous to assume delete is ok
			logrus.WithFields(logrus.Fields{
				"container": c.Name,
				"old":       oldC.ID,
				"new":       c.ID,
			}).Warn("overwriting existing container")
		} else {
			return nil, false, fmt.Errorf("machine %q already exists", c.Name)
		}
	}
	m.insertContainerLocked(c)

	shouldStart := record.State == types.ContainerStateRunning
	if record.State == types.ContainerStateDeleting {
		go func() {
			err := c.Delete()
			if err != nil {
				logrus.WithError(err).WithField("container", c.Name).Error("failed to delete container")
			}
		}()
	}

	go func() {
		err := m.onRestoreContainer(c)
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("container restore hook failed")
		}
	}()

	return c, shouldStart, nil
}

func (c *Container) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.startLocked(false /* isInternal */)
}

func (c *Container) startLocked(isInternal bool) (err error) {
	if c.lxc.Running() {
		return nil
	}

	if c.manager.stopping {
		return ErrStopping
	}

	oldState, err := c.setStateInternalLocked(types.ContainerStateStarting, isInternal)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
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

	// hook
	if c.hooks != nil {
		err := c.hooks.PreStart(c)
		if err != nil {
			return err
		}
	}

	err = c.lxc.Start()
	if err != nil {
		return fmt.Errorf("start '%s': %w", c.Name, err)
	}

	if !c.lxc.Wait(lxc.RUNNING, startTimeout) {
		return fmt.Errorf("machine did not start: %s - %v", c.Name, c.lxc.State())
	}

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
		err := c.addDeviceNodeLocked(src, src)
		if err != nil {
			return err
		}
	}

	err = c.onStartLocked()
	if err != nil {
		return err
	}

	logrus.WithField("container", c.Name).Info("container started")

	return nil
}

func (c *Container) onStartLocked() error {
	_, err := c.setStateLocked(types.ContainerStateRunning)
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
	targetLen := len(agent.ProcessName)
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
	cmd := &LxcCommand{
		CombinedArgs: []string{padAgentCmd("/proc/self/fd/" + strconv.Itoa(exeFd))},
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
	c.agent.Set(client)

	// async tasks, so we can release the lock
	// will block if agent is broken
	// also, we'll close our side of the remote fd so RPC will return ECONNRESET
	go func() {
		err := c.initAgent(client)
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to init agent")
		}
	}()

	return nil
}

func (c *Container) initAgent(a *agent.Client) error {
	// inet diag
	initPidF, err := c.lxc.InitPidFd()
	if err != nil {
		return err
	}
	defer initPidF.Close()

	nlFile, err := sysnet.WithNetns(initPidF, func() (*os.File, error) {
		return sysnet.OpenDiagNetlink()
	})
	if err != nil {
		return err
	}
	c.inetDiagFile = nlFile

	go runOne("netlink monitor for "+c.Name, func() error {
		return monitorInetDiag(c, nlFile)
	})

	// update listeners in case we missed any before agent start
	c.triggerListenersUpdate()

	// ssh agent proxy (vscode workaround)
	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return err
	}
	err = a.StartSshAgentProxy(agent.SshAgentProxyArgs{
		Uid: hostUser.Uid,
		Gid: hostUser.Uid,
	})
	if err != nil {
		return err
	}

	// bind mount NFS if ok (i.e. if host already did)
	if c.manager.hostNfsMounted {
		err = a.BindMountNfsRoot(agent.BindMountArgs{
			Source: "/mnt/machines",
			Target: hostUser.HomeDir + "/" + mounts.NfsDirName,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
