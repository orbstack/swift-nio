package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"

	_ "net/http/pprof"

	"github.com/orbstack/macvirt/macvmgr/conf/mounts"
	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/macvmgr/logutil"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/killswitch"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
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

	// setup and start nfs uid
	if conf.C().StartNfs {
		// chown nfs root (perm mode = 700)
		err = os.Chown(conf.C().NfsRootRW, u.Uid, u.Uid)
		if err != nil {
			return err
		}

		// create docker nfs volumes subdir (because it's referenced in exports below)
		err = os.MkdirAll(conf.C().NfsRootRW+"/"+nfsDockerSubdir, 0755)
		if err != nil {
			return err
		}

		// we create two exports:
		// 1. root export, for linux machines (fsid=0): squash uid to host user
		// this makes sure copied files have correct ownership
		// 2. docker export, for docker volumes (fsid=1): squash uid to root
		// most docker volumes are owned by root and some have restrictive perms
		// so this ensures people can actually use them, e.g. in finder (which can't use sudo)
		nfsExport := fmt.Sprintf("/nfsroot-ro 127.0.0.8(rw,async,fsid=0,crossmnt,insecure,all_squash,no_subtree_check,anonuid=%d,anongid=%d)\n/nfsroot-ro/docker 127.0.0.8(rw,async,fsid=1,crossmnt,insecure,all_squash,no_subtree_check,anonuid=0,anongid=0)", u.Uid, u.Uid)
		//err = util.RunCmd("exportfs", "-o", "rw,async,fsid=0,crossmnt,insecure,all_squash,no_subtree_check,anonuid="+strconv.Itoa(u.Uid)+",anongid="+strconv.Itoa(u.Uid), nfsExport)
		err = os.WriteFile(conf.C().EtcExports, []byte(nfsExport), 0644)
		if err != nil {
			return err
		}
		go func() {
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
		err := util.RunInheritOut("btrfs", "filesystem", "resize", "max", conf.C().DataFsDir)
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

	// rand seed no longer needed in go 1.20+

	// killswitch
	logrus.Info("starting")
	err := killswitch.Check()
	check(err)

	// connect to hcontrol (ownership taken by cmgr)
	logrus.Debug("connecting to hcontrol")
	hcontrolConn, err := net.Dial("tcp", conf.C().HcontrolIP+":"+strconv.Itoa(ports.SecureSvcHcontrol))
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
