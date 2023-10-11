package tcppump

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/sirupsen/logrus"
)

func pump1SshAgent(errc chan<- error, src, dst FullDuplexConn) {
	// Workaround for NFS panic
	defer func() {
		if err := recover(); err != nil {
			errc <- fmt.Errorf("tcp pump1: panic: %v", err)
		}
	}()

	// Workaround for Secretive agent bug: https://github.com/maxgoedjen/secretive/issues/483
	// read full ssh agent messages w/ length, and write them atomically, to prevent agent from getting a short read
	var err error
	for {
		var lenBuf [4]byte
		_, err = io.ReadFull(src, lenBuf[:])
		if err != nil {
			break
		}

		len := binary.BigEndian.Uint32(lenBuf[:])

		msgBuf := make([]byte, 4+len)
		copy(msgBuf[:4], lenBuf[:])
		_, err = io.ReadFull(src, msgBuf[4:])
		if err != nil {
			break
		}

		_, err = dst.Write(msgBuf)
		if err != nil {
			break
		}
	}

	// half-close to allow graceful shutdown
	dst.CloseWrite()
	// this is useless, doesn't send anything, but it's a good precaution
	src.CloseRead()

	if err == io.EOF {
		err = nil
	}

	errc <- err
}

func Pump2SshAgent(c1, c2 FullDuplexConn) {
	errChan := make(chan error, 2)
	go pump1SshAgent(errChan, c1, c2)
	go pump1SshAgent(errChan, c2, c1)

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
