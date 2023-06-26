package tcpfwd

import (
	"fmt"
	"io"
	"net"

	"github.com/sirupsen/logrus"
)

type FullDuplexConn interface {
	net.Conn
	CloseRead() error
	CloseWrite() error
}

func pump1(errc chan<- error, src, dst FullDuplexConn) {
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
	// this is useless, doesn't send anything, but it's a good precaution
	src.CloseRead()

	errc <- err
}

func pump2(c1, c2 FullDuplexConn) {
	errChan := make(chan error, 2)
	go pump1(errChan, c1, c2)
	go pump1(errChan, c2, c1)

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
