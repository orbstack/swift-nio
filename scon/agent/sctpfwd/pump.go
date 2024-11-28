package sctpfwd

import (
	"io"
	"net"
)

func pump1(errc chan<- error, src, dst net.Conn) {
	// SCTP packets are limited to 16 KiB, but large messages up to 256 KiB (officially) and 1 GiB (unofficially) can be fragmented
	// no one uses SCTP, so just use a large buffer to handle this
	buf := make([]byte, 256*1024)
	_, err := io.CopyBuffer(dst, src, buf)

	errc <- err
}

func pump2(c1, c2 net.Conn) {
	defer c1.Close()
	defer c2.Close()

	errChan := make(chan error, 2)
	go pump1(errChan, c1, c2)
	go pump1(errChan, c2, c1)

	// unlike TCP, SCTP has no half-open state, so we have to close both sides if either side returns
	<-errChan
}
