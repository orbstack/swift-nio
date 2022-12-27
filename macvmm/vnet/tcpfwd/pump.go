package tcpfwd

import (
	"fmt"
	"io"
	"net"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	log "github.com/sirupsen/logrus"
)

func pump1(errc chan<- error, src, dst net.Conn) {
	// Workaround for NFS panic
	defer func() {
		if err := recover(); err != nil {
			log.Errorf("tcp pump1: panic: %v", err)
			errc <- fmt.Errorf("tcp pump1: panic: %v", err)
		}
	}()

	buf := make([]byte, 512*1024)
	_, err := io.CopyBuffer(dst, src, buf)

	// half-close to allow graceful shutdown
	if dstTcp, ok := dst.(*net.TCPConn); ok {
		dstTcp.CloseWrite()
	}
	if dstTcp, ok := dst.(*gonet.TCPConn); ok {
		dstTcp.CloseWrite()
	}

	if srcTcp, ok := src.(*net.TCPConn); ok {
		srcTcp.CloseRead()
	}
	if srcTcp, ok := src.(*gonet.TCPConn); ok {
		srcTcp.CloseRead()
	}

	errc <- err
}

func pump2(c1, c2 net.Conn) {
	errChan := make(chan error, 2)
	go pump1(errChan, c1, c2)
	go pump1(errChan, c2, c1)

	// Don't wait for both if one side failed (not EOF)
	if err1 := <-errChan; err1 != nil {
		return
	}
	if err2 := <-errChan; err2 != nil {
		return
	}
}
