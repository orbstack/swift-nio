package dmigrate

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	vmgrutil "github.com/orbstack/macvirt/vmgr/util"
	"github.com/sirupsen/logrus"
)

func (m *Migrator) stopSrc(srcSocket string) error {
	// open a conn to ddesktop socket, so we know when it's stopped
	desktopConn, err := net.Dial("unix", srcSocket)
	if err != nil {
		return fmt.Errorf("open desktop conn: %w", err)
	}
	defer desktopConn.Close()

	// always quit remote so we can change context back after it stops
	logrus.Info("Stopping Docker Desktop")
	err = util.Run("osascript", "-e", `quit app "Docker Desktop"`)
	if err != nil {
		return fmt.Errorf("quit app: %w", err)
	}

	// wait for it to stop
	err = vmgrutil.WithTimeout1(func() error {
		var buf [1]byte
		_, err := desktopConn.Read(buf[:])
		return err
	}, RemoteStopTimeout)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// EOF = stopped

		// wait a bit for any processes to exit
		time.Sleep(1 * time.Second)

		// restore context for more seamless migration end
		err = util.Run(conf.FindXbin("docker"), "context", "use", appid.AppName)
		if err != nil {
			return fmt.Errorf("set docker context: %w", err)
		}
	} else {
		logrus.Warnf("Docker Desktop did not stop in time: %v", err)
	}

	return nil
}

func (m *Migrator) Finalize() error {
	err := m.stopSrc(m.srcSocketPath)
	if err != nil {
		return fmt.Errorf("stop docker desktop: %w", err)
	}

	// start any containers that were running on src
	for _, cid := range m.destContainersToStart {
		err := m.destClient.StartContainer(cid)
		if err != nil {
			return fmt.Errorf("start container: %w", err)
		}
	}

	return nil
}
