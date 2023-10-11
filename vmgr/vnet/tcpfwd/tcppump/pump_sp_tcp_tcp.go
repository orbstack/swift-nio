package tcppump

import (
	"fmt"
	"net"

	"github.com/sirupsen/logrus"
)

// monomorphized copy of pump.go
func pump1SpTcpTcp(errc chan<- error, src *net.TCPConn, dst *net.TCPConn) {
	// Workaround for NFS panic
	defer func() {
		if err := recover(); err != nil {
			errc <- fmt.Errorf("tcp pump1: panic: %v", err)
		}
	}()

	_, err := CopyBuffer(dst, src, nil)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errc <- err
}

func Pump2SpTcpTcp(c1 *net.TCPConn, c2 *net.TCPConn) {
	errChan := make(chan error, 2)
	go pump1SpTcpTcp(errChan, c1, c2)
	go pump1SpTcpTcp(errChan, c2, c1)

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
