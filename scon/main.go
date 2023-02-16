package main

import (
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"time"

	_ "net/http/pprof"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/kdrag0n/macvirt/scon/killswitch"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	AppName = "scon"

	cmdContainerManager = "container-manager"
	cmdForkStart        = "fork-start"
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

func doSystemInitTasks(host *hclient.Client) error {
	// get user
	u, err := host.GetUser()
	if err != nil {
		return err
	}

	// chown secure sockets
	for _, sock := range []string{
		mounts.SshAgentSocket,
		mounts.HostSSHSocket,
		mounts.HcontrolSocket,
	} {
		// use user group
		err = os.Chown(sock, u.Uid, u.Uid)
		if err != nil {
			return err
		}
	}

	// setup and start nfs uid
	if conf.C().StartNfs {
		nfsExport := fmt.Sprintf("/nfsroot-ro 127.0.0.8(rw,async,fsid=0,crossmnt,insecure,all_squash,no_subtree_check,anonuid=%d,anongid=%d)\n", u.Uid, u.Uid)
		//err = util.RunCmd("exportfs", "-o", "rw,async,fsid=0,crossmnt,insecure,all_squash,no_subtree_check,anonuid="+strconv.Itoa(u.Uid)+",anongid="+strconv.Itoa(u.Uid), nfsExport)
		err = os.WriteFile(conf.C().EtcExports, []byte(nfsExport), 0644)
		if err != nil {
			return err
		}
		go func() {
			err := util.RunInheritOut("/opt/vc/vinit-nfs")
			if err != nil {
				logrus.WithError(err).Error("failed to start nfs")
			}
		}()
	}

	return nil
}

func runContainerManager() {
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
	}
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02 15:04:05",
	})

	// rand seed
	rand.Seed(time.Now().UnixNano())

	// killswitch
	err := killswitch.Check()
	check(err)

	// connect to hcontrol (ownership taken by cmgr)
	if conf.C().DummyHcontrol {
		err := hclient.StartDummyServer()
		check(err)
	}
	logrus.Debug("connecting to hcontrol")
	hcontrolConn, err := net.Dial("tcp", conf.C().HcontrolIP+":"+strconv.Itoa(ports.SecureSvcHcontrol))
	check(err)
	hc, err := hclient.New(hcontrolConn)
	check(err)

	// system init tasks
	err = doSystemInitTasks(hc)
	check(err)

	// start container manager
	mgr, err := NewConManager(conf.C().SconDataDir, hc)
	check(err)
	defer func() {
		if mgr.pendingVMShutdown {
			cmd := exec.Command("poweroff")
			err := cmd.Start()
			if err != nil {
				logrus.WithError(err).Error("failed to run poweroff")
			}

			go func() {
				time.Sleep(2 * time.Minute)
				err := util.Run("poweroff", "-f")
				if err != nil {
					logrus.WithError(err).Error("failed to force poweroff (fallback)")
				}
			}()
		}
	}()
	defer mgr.Close()
	mgr.Start()
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
	case cmdForkStart:
		runForkStart()
	case cmdLxcHook:
		runLxcHook()
	default:
		panic("unknown command: " + cmd)
	}
}
