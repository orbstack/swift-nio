package tcpfwd

import (
	"io"
	"net"

	"github.com/sirupsen/logrus"
)

func pump1SpTcpUnix(errC chan<- error, src *net.TCPConn, dst *net.UnixConn) {
	buf := make([]byte, bufferSize)
	_, err := io.CopyBuffer(dst, src, buf)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errC <- err
}

func pump1SpUnixTcp(errC chan<- error, src *net.UnixConn, dst *net.TCPConn) {
	buf := make([]byte, bufferSize)
	_, err := io.CopyBuffer(dst, src, buf)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errC <- err
}

func Pump2SpTcpUnix(c1 *net.TCPConn, c2 *net.UnixConn) {
	errChan := make(chan error, 2)
	go pump1SpTcpUnix(errChan, c1, c2)
	go pump1SpUnixTcp(errChan, c2, c1)

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
