package main

import (
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	_ "net/http/pprof"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	AppName = "scon"

	cmdContainerManager = "container-manager"
	cmdForkStart        = "fork-start"
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

func runContainerManager() {
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "01-02 15:04:05",
		})
	}

	// rand seed
	rand.Seed(time.Now().UnixNano())

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

	// start container manager
	mgr, err := NewConManager(conf.C().SconDataDir, hc)
	check(err)
	defer mgr.Close()
	mgr.Start()
	check(err)

	// services
	if conf.Debug() {
		go runPprof()
	}

	go func() {
		err := runCliTest(mgr)
		if err != nil {
			logrus.WithError(err).Error("cli test failed")
			mgr.Close()
		}
	}()

	// listen for signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, unix.SIGINT, unix.SIGTERM)
	select {
	case <-sigChan:
	case <-mgr.stopChan:
	}

	logrus.Info("shutting down")
}

func runCliTest(mgr *ConManager) error {
	var err error

	container, ok := mgr.GetByName("ubuntu-x86")
	if !ok {
		// create
		fmt.Println("create")
		container, err = mgr.Create(CreateParams{
			Name: "ubuntu-x86",
			Image: ImageSpec{
				Distro:  "ubuntu",
				Version: "kinetic",
				Arch:    "amd64",
			},
			UserPassword: "test",
		})
		if err != nil {
			return err
		}
	}

	fmt.Println("start")
	err = container.Start()
	if err != nil {
		return err
	}

	return nil
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
	default:
		panic("unknown command: " + cmd)
	}
}
