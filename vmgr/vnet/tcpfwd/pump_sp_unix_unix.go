package tcpfwd

import (
	"fmt"
	"net"

	"github.com/sirupsen/logrus"
)

// monomorphized copy of pump.go
func pump1SpUnixUnix(errc chan<- error, src *net.UnixConn, dst *net.UnixConn) {
	// Workaround for NFS panic
	defer func() {
		if err := recover(); err != nil {
			errc <- fmt.Errorf("tcp pump1: panic: %v", err)
		}
	}()

	_, err := pumpCopyBuffer(dst, src, nil)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errc <- err
}

func pump2SpUnixUnix(c1 *net.UnixConn, c2 *net.UnixConn) {
	errChan := make(chan error, 2)
	go pump1SpUnixUnix(errChan, c1, c2)
	go pump1SpUnixUnix(errChan, c2, c1)

	// Don't wait for both if one side failed (not EOF)
	if err1 := <-errChan; err1 != nil {
		logrus.WithError(err1).Debug("tcp pump2 error 1")
		return
	}
	if err2 := <-errChan; err2 != nil {
		logrus.WithError(err2).Debug("tcp pump2 error 2")
		return
	}
}
