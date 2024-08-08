package readyclient

import (
	"net"
	"strconv"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/sirupsen/logrus"
)

const (
	ServiceSconRPC         = "sconrpc"
	ServiceSconRPCInternal = "sconrpc-internal"
	ServiceKrpc            = "krpc"
)

func reportReadySync(serverAddr string, serviceName string) error {
	addr := net.JoinHostPort(serverAddr, strconv.Itoa(ports.SecureSvcReadyEvents))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}

	_, err = conn.Write([]byte(serviceName + "\n"))
	if err != nil {
		return err
	}

	return nil
}

// reporting can be done asynchronously to avoid blocking startup
func ReportReady(serverAddr string, serviceName string) {
	go func() {
		logrus.WithField("service", serviceName).Debug("reporting service ready")
		err := reportReadySync(serverAddr, serviceName)
		if err != nil {
			logrus.WithError(err).Error("failed to report service ready")
		}
	}()
}
