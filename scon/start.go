package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	startTimeout = 10 * time.Second
)

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

	c.lxcParams.Configs = append(c.lxcParams.Configs, KeyValue[string]{key, value})
	return nil
}

func (c *Container) initLxc() error {
	m := c.manager
	lc, err := lxc.NewContainer(c.Name, m.lxcDir)
	if err != nil {
		return err
	}
	runtime.SetFinalizer(lc, func(lc *lxc.Container) {
		lc.Release()
	})
	c.lxc = lc

	// logging
	logPath := m.subdir("logs") + "/" + c.ID + ".log"
	lc.ClearConfig()
	lc.SetLogFile(logPath)
	c.lxcParams = LxcForkParams{
		Name:    c.Name,
		LxcDir:  m.lxcDir,
		LogFile: logPath,
	}
	if conf.Debug() {
		lc.SetVerbosity(lxc.Verbose)
		lc.SetLogLevel(lxc.TRACE)
		c.lxcParams.Verbosity = lxc.Verbose
		c.lxcParams.LogLevel = lxc.TRACE
	} else {
		lc.SetVerbosity(lxc.Quiet)
		lc.SetLogLevel(lxc.INFO)
		c.lxcParams.Verbosity = lxc.Quiet
		c.lxcParams.LogLevel = lxc.INFO
	}

	// configs
	rootfs := path.Join(c.dir, "rootfs")
	err = os.MkdirAll(rootfs, 0755)
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

	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return err
	}

	// set configs!
	err = func() error {
		defer func() {
			if err2 := recover(); err2 != nil {
				err = fmt.Errorf("failed to set LXC config: %v", err2)
			}
		}()

		set := func(key, value string) {
			if err := c.setLxcConfig(key, value); err != nil {
				panic(err)
			}
		}

		addDev := func(node string) {
			if err := addInitDevice(c, node); err != nil {
				panic(err)
			}
		}
		bind := func(src, dst, opts string) {
			if err := addInitBindMount(c, src, dst, opts); err != nil {
				panic(err)
			}
		}

		/*
		* from LXD
		 */
		set("lxc.pty.max", "1024")
		set("lxc.tty.max", "0")
		// limiting caps breaks privileged nested docker containers, even if only sys_time
		//set("lxc.cap.drop", "sys_time sys_module sys_rawio mac_admin mac_override")
		set("lxc.autodev", "1") // populate /dev

		// console
		set("lxc.console.logfile", logPath+"-console")
		set("lxc.console.buffer.size", "auto")
		set("lxc.console.size", "auto")

		set("lxc.cgroup2.devices.deny", "a")
		set("lxc.cgroup2.devices.allow", "b *:* m")     // mknod block
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
		set("lxc.cgroup2.devices.allow", "c 7:* rwm")   // dev/loop*

		// Devices
		addDev("/dev/fuse")
		addDev("/dev/net/tun")
		addDev("/dev/ppp")
		addDev("/dev/kmsg")
		addDev("/dev/loop-control")
		addDev("/dev/autofs") // TODO security
		addDev("/dev/userfaultfd")

		// Default mounts
		set("lxc.mount.auto", "proc:mixed sys:mixed cgroup:rw:force")
		set("lxc.mount.entry", "mqueue dev/mqueue mqueue rw,relatime,create=dir,optional 0 0")
		// don't let people mess with binfmt_misc
		//set("lxc.mount.entry", "/proc/sys/fs/binfmt_misc proc/sys/fs/binfmt_misc none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "/sys/fs/fuse/connections sys/fs/fuse/connections none rbind,create=dir,optional 0 0")
		set("lxc.mount.entry", "/sys/kernel/security sys/kernel/security none rbind,create=dir,optional 0 0")

		// nesting
		set("lxc.mount.entry", "proc dev/.lxc/proc proc create=dir,optional 0 0")
		set("lxc.mount.entry", "sys dev/.lxc/sys sysfs create=dir,optional 0 0")

		// tmpfs
		set("lxc.mount.entry", "tmpfs tmp tmpfs rw,nosuid,nodev,nr_inodes=1048576,inode64,create=dir,optional 0 0")

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

		// bind mounts
		config := conf.C()
		bind(config.GuestMountSrc, "/opt/macvirt-guest", "ro")
		bind(config.HostMountSrc, "/mnt/mac", "")
		// we're doing this in kernel now, to avoid showing up in `df`
		//bind(config.FakeSrc+"/sysctl/kernel.panic", "/proc/sys/kernel/panic", "ro")

		// binds for mac linked paths
		// symlinks cause problems with vs code, git, etc. so we bind them
		for _, p := range mounts.LinkedPaths {
			bind("/mnt/mac"+p, p, "")
		}
		bind("/mnt/mac", "/mac", "")

		// binds for ssh agent sockets (fixes docker $SSH_AUTH_SOCK forward)
		// anything operation (mount, stat, access) on the /private socket through virtiofs returns EOPNOTSUPP
		// so we bind the dir to a tmpfs
		if sshAgentSocks.Env != "" && strings.HasPrefix(sshAgentSocks.Env, "/private/tmp/com.apple.launchd.") {
			bind(mounts.LaunchdSshAgentListeners, path.Dir(sshAgentSocks.Env), "ro")
		}

		// log
		set("lxc.log.file", logPath)
		if conf.Debug() {
			set("lxc.log.level", "trace")
		} else {
			set("lxc.log.level", "info")
		}

		// container hooks, before rootfs is set
		if c.hooks != nil {
			newRootfs, err := c.hooks.Config(c, set)
			if err != nil {
				panic(err)
			}
			if newRootfs != "" {
				rootfs = newRootfs
			}

			// bind for docker (TODO move into hook)
			bind(config.NfsRootRO, hostUser.HomeDir+"/"+mounts.NfsDirName, "ro,rshared")
		}

		// container
		set("lxc.rootfs.path", "dir:"+rootfs)
		set("lxc.uts.name", c.Name)

		// hooks
		set("lxc.hook.version", "1")
		set("lxc.hook.post-stop", fmt.Sprintf("%s %s %s %s", exePath, cmdLxcHook, lxcHookPostStop, c.ID))

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

func (m *ConManager) restoreContainers() ([]*Container, error) {
	records, err := m.db.GetContainers()
	if err != nil {
		return nil, err
	}

	// inject builtin
	copy := dockerContainerRecord
	// prepend
	records = append([]*types.ContainerRecord{&copy}, records...)

	var pendingStarts []*Container
	for _, record := range records {
		c, shouldStart, err := m.restoreOne(record)
		if err != nil {
			logrus.WithError(err).WithField("container", record.Name).Error("failed to restore container")
			continue
		}

		logrus.WithField("container", c.Name).WithField("record", record).Debug("restored container")
		if shouldStart {
			pendingStarts = append(pendingStarts, c)
		}
	}

	return pendingStarts, nil
}

func (m *ConManager) insertContainer(c *Container) {
	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	m.containersByID[c.ID] = c
	m.containersByName[c.Name] = c
}

func (m *ConManager) restoreOne(record *types.ContainerRecord) (*Container, bool, error) {
	c, err := m.newContainer(record)
	if err != nil {
		return nil, false, err
	}

	m.insertContainer(c)

	shouldStart := record.Running && !record.Deleting
	if record.Deleting {
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

func (c *Container) forkStart() error {
	paramsData, err := gobEncode(&c.lxcParams)
	if err != nil {
		return err
	}

	// base64
	paramsB64 := base64.StdEncoding.EncodeToString(paramsData)

	// fork
	cmd := exec.Command("/proc/self/exe", cmdForkStart, paramsB64)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) updateTimeOffsets() error {
	var tsMono unix.Timespec
	err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &tsMono)
	if err != nil {
		return err
	}

	var tsBoot unix.Timespec
	err = unix.ClockGettime(unix.CLOCK_BOOTTIME, &tsBoot)
	if err != nil {
		return err
	}

	nsMono := uint64(tsMono.Sec)*1e9 + uint64(tsMono.Nsec)
	nsBoot := uint64(tsBoot.Sec)*1e9 + uint64(tsBoot.Nsec)
	err = c.setLxcConfig("lxc.time.offset.monotonic", strconv.FormatUint(nsMono, 10)+"ns")
	if err != nil {
		return err
	}

	err = c.setLxcConfig("lxc.time.offset.boot", strconv.FormatUint(nsBoot, 10)+"ns")
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) Start() error {
	if c.manager.stopping {
		return errors.New("machine manager is stopping")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.lxc.Running() {
		return nil
	}

	logrus.WithField("container", c.Name).Info("starting container")

	// update time offsets
	/*
		err := c.updateTimeOffsets()
		if err != nil {
			return err
		}
	*/

	// clean console
	_ = os.Remove(c.logPath() + "-console")

	// hook
	if c.hooks != nil {
		err := c.hooks.PreStart(c)
		if err != nil {
			return err
		}
	}

	err := c.forkStart()
	if err != nil {
		return err
	}

	if !c.lxc.Wait(lxc.RUNNING, startTimeout) {
		return fmt.Errorf("container did not start: %s - %v", c.Name, c.lxc.State())
	}

	go func() {
		err := c.startAgent()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to start agent")
		}
	}()

	err = c.onStart()
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) onStart() error {
	c.state = ContainerStateRunning

	// update & persist state
	err := c.persist()
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

func (c *Container) startAgent() error {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	// Stop() hangs without this
	go cmd.Process.Wait()

	// agent client
	client := agent.NewClient(cmd.Process, rpcConn, fdxConn)
	c.agent.Set(client)

	// async tasks, so we can release the lock
	// will block if agent is broken
	// also, we'll close our side of the remote fd so RPC will return ECONNRESET
	go func() {
		err := c.initAgent()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to init agent")
		}
	}()

	return nil
}

func (c *Container) initAgent() error {
	// inet diag
	nlFile, err := c.Agent().OpenDiagNetlink()
	if err != nil {
		return err
	}

	go runOne("netlink monitor for "+c.Name, func() error {
		return monitorInetDiag(c, nlFile)
	})

	// update listeners in case we missed any before agent start
	c.triggerListenersUpdate()

	// ssh agent proxy (vscode workaround)
	u, err := c.manager.host.GetUser()
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	err = c.Agent().StartSshAgentProxy(agent.SshAgentProxyArgs{
		Uid: uid,
		Gid: uid,
	})
	if err != nil {
		return err
	}

	return nil
}
