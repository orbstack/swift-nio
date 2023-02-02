package tcpfwd

import (
	"fmt"
	"io"
	"net"

	"github.com/sirupsen/logrus"
)

const (
	//TODO
	bufferSize = 256 * 1024
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

	buf := make([]byte, bufferSize)
	_, err := io.CopyBuffer(dst, src, buf)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errc <- err
}

func Pump2(c1, c2 FullDuplexConn) {
	errChan := make(chan error, 2)
	go pump1(errChan, c1, c2)
	go pump1(errChan, c2, c1)

	// Don't wait for both if one side failed (not EOF)
	if err1 := <-errChan; err1 != nil {
		logrus.Error("tcp pump2 error 1 ", err1)
		return
	}
	if err2 := <-errChan; err2 != nil {
		logrus.Error("tcp pump2 error 2 ", err2)
		return
	}
}
