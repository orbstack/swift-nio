package tcpfwd

import (
	"fmt"
	"io"
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
)

// monomorphized copy of pump.go
func pump1SpUnixGv(errc chan<- error, src *net.UnixConn, dst *gonet.TCPConn) {
	// Workaround for NFS panic
	defer func() {
		if err := recover(); err != nil {
			errc <- fmt.Errorf("tcp pump1: panic: %v", err)
		}
	}()

	buf := make([]byte, 512*1024)
	_, err := io.CopyBuffer(dst, src, buf)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errc <- err
}

func pump1SpGvUnix(errc chan<- error, src *gonet.TCPConn, dst *net.UnixConn) {
	// Workaround for NFS panic
	defer func() {
		if err := recover(); err != nil {
			errc <- fmt.Errorf("tcp pump1: panic: %v", err)
		}
	}()

	buf := make([]byte, 512*1024)
	_, err := io.CopyBuffer(dst, src, buf)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errc <- err
}

func pump2SpUnixGv(c1 *net.UnixConn, c2 *gonet.TCPConn) {
	errChan := make(chan error, 2)
	go pump1SpUnixGv(errChan, c1, c2)
	go pump1SpGvUnix(errChan, c2, c1)

	// Don't wait for both if one side failed (not EOF)
	if err1 := <-errChan; err1 != nil {
		logrus.WithError(err1).Error("tcp pump2 error 1")
		return
	}
	if err2 := <-errChan; err2 != nil {
		logrus.WithError(err2).Error("tcp pump2 error 2")
		return
	}
}
