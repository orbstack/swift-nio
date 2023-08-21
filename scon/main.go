package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"

	_ "net/http/pprof"

	"github.com/getsentry/sentry-go"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/killswitch"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/btrfs"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/appver"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/conf/sentryconf"
	"github.com/orbstack/macvirt/vmgr/logutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	AppName = "scon"

	cmdContainerManager = "mgr"
	cmdLxcHook          = "lxc-hook"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func runPprof() {
	err := http.ListenAndServe("localhost:6060", nil)
	if err != nil {
		logrus.WithError(err).Error("failed to start pprof server")
	}
}

func doSystemInitTasks(mgr *ConManager, host *hclient.Client) error {
	// get user
	u, err := host.GetUser()
	if err != nil {
		return err
	}

	// TODO
	mgr.k8sEnabled = true

	// start host service proxies now that we have uid/gid
	go runOne("host service proxy host-ssh", func() error {
		return RunHostServiceProxy(mounts.HostSSHSocket, ports.SecureSvcHostSSH, u.Uid)
	})
	go runOne("host service proxy hcontrol", func() error {
		return RunHostServiceProxy(mounts.HcontrolSocket, ports.SecureSvcHcontrol, u.Uid)
	})
	go runOne("host service proxy ssh-agent", func() error {
		return RunHostServiceProxy(mounts.SshAgentSocket, ports.SecureSvcHostSSHAgent, u.Uid)
	})
	go runOne("vscode ssh agent proxy", func() error {
		return RunSshAgentProxy(u.Uid, u.Gid)
	})

	// perms for cmdlinks
	err = os.Chown(conf.C().CmdLinksDir, u.Uid, u.Gid)
	if err != nil {
		return err
	}

	// setup and start nfs uid
	if conf.C().StartNfs {
		// chown nfs roots (perm mode = 700)
		// we use correct gid here to avoid wrong "macports" group on Mac: https://github.com/orbstack/orbstack/issues/404
		err = os.Chown(nfsDirRoot+"/rw", u.Uid, u.Gid)
		if err != nil {
			return err
		}
		err = os.Chown(nfsDirForMachines+"/rw", u.Uid, u.Gid)
		if err != nil {
			return err
		}

		// we create two exports:
		// 1. root export, for linux machines (fsid=0): squash uid to host user
		// this makes sure copied files have correct ownership
		// 2. docker export, for docker volumes (fsid=1): squash uid to root
		// most docker volumes are owned by root and some have restrictive perms
		// so this ensures people can actually use them, e.g. in finder (which can't use sudo)
		// plus images and containers, which use mergerfs to collapse inodes from underlying overlayfs and avoid polluting mounts / causing flaky rpc.mountd NFS4ERR_DELAY issues
		mgr.nfsRoot.hostUid = u.Uid
		go func() {
			// update exports
			err := mgr.nfsRoot.updateExports()
			if err != nil {
				logrus.WithError(err).Error("failed to update nfs exports")
				return
			}

			// don't init nfs more than once. causes issues with exports
			if data, err := os.ReadFile("/proc/fs/nfsd/portlist"); err == nil && len(strings.TrimSpace(string(data))) > 0 {
				logrus.Debug("nfs already initialized")
			} else {
				err := util.RunInheritOut("/opt/orb/vinit-nfs")
				if err != nil {
					logrus.WithError(err).Error("failed to start nfs")
					return
				}

				// report nfs ready
				err = host.OnNfsReady()
				if err != nil {
					logrus.WithError(err).Error("failed to mount nfs on host")
					return
				}
			}

			// bind into containers
			err = mgr.onHostNfsMounted()
			if err != nil {
				logrus.WithError(err).Error("failed to bind nfs after mount")
				return
			}
		}()
	}

	go runOne("resize fs", func() error {
		// resize filesystem
		logrus.Debug("resizing filesystem")
		err := btrfs.FilesystemResize(conf.C().DataFsDir, "max")
		if err != nil {
			return err
		}

		// report fs ready
		err = host.OnDataFsReady()
		if err != nil {
			return err
		}

		// syncfs on fd
		fd, err := unix.Open(conf.C().DataFsDir, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open fs dir: %w", err)
		}
		defer unix.Close(fd)

		err = unix.Syncfs(fd)
		if err != nil {
			return fmt.Errorf("syncfs: %w", err)
		}

		return nil
	})

	return nil
}

func runContainerManager() {
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
	}
	logrus.SetFormatter(logutil.NewPrefixFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02 15:04:05",
		DisableColors:   slices.Contains(os.Args[1:], "orb.console_is_pipe"),
	}, "📦 scon | "))

	logrus.Info("starting")

	if !conf.Debug() {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:     sentryconf.DSN,
			Release: appver.Get().Short,
		})
		if err != nil {
			logrus.WithError(err).Error("failed to init Sentry")
		}
		defer sentry.Flush(sentryconf.FlushTimeout)
	}
	// sentry.Recover() suppresses panic
	defer func() {
		if err := recover(); err != nil {
			hub := sentry.CurrentHub()
			hub.Recover(err)

			panic(err)
		}
	}()

	// killswitch
	err := killswitch.Check()
	check(err)

	// connect to hcontrol (ownership taken by cmgr)
	logrus.Debug("connecting to hcontrol")
	hcontrolConn, err := netx.Dial("tcp", conf.C().HcontrolIP+":"+strconv.Itoa(ports.SecureSvcHcontrol))
	check(err)
	hostClient, err := hclient.New(hcontrolConn)
	check(err)

	// create container manager
	mgr, err := NewConManager(conf.C().SconDataDir, hostClient)
	check(err)

	// system init tasks
	err = doSystemInitTasks(mgr, hostClient)
	check(err)

	defer mgr.Close()
	err = mgr.Start()
	check(err)

	// services
	if conf.Debug() {
		go runPprof()
	}

	// listen for signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, unix.SIGINT, unix.SIGTERM, unix.SIGQUIT)
	select {
	case <-sigChan:
	case <-mgr.stopChan:
	}

	logrus.Info("shutting down")
}

func main() {
	cmd := cmdContainerManager
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case cmdContainerManager:
		runContainerManager()
	case cmdLxcHook:
		runLxcHook()
	default:
		panic("unknown command: " + cmd)
	}
}
