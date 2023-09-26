package tcpfwd

import (
	"fmt"
	"io"
	"net"

	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
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

	_, err := pumpCopyBuffer(dst, src, nil)

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	src.CloseRead()

	errc <- err
}

func copyViewBufferGvUnix(dst *net.UnixConn, src *gonet.TCPConn, vw *gonet.ViewWriter) (written int64, err error) {
	for {
		vw.Reset(zeroCopyGvBufferSize)
		_nr, er := src.ReadViews(vw)
		nr := int64(_nr)
		if nr > 0 {
			buffers := vw.Buffers()
			nw, ew := buffers.WriteTo(dst)
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errInvalidWrite
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

func pump1SpGvUnix(errc chan<- error, src *gonet.TCPConn, dst *net.UnixConn) {
	// Workaround for NFS panic
	defer func() {
		if err := recover(); err != nil {
			errc <- fmt.Errorf("tcp pump1: panic: %v", err)
		}
	}()

	vw := gonet.NewViewWriter(2)
	defer vw.Reset(0)
	_, err := copyViewBufferGvUnix(dst, src, vw)

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
		logrus.WithError(err1).Debug("tcp pump2 error 1")
		return
	}
	if err2 := <-errChan; err2 != nil {
		logrus.WithError(err2).Debug("tcp pump2 error 2")
		return
	}
}
