package util

import (
	"net"
	"sync/atomic"
)

type DispatchedListener struct {
	closed atomic.Bool

	addrFunc func() net.Addr

	connQueue  chan net.Conn
	errorQueue chan error
}

// check interface conformance
var _ net.Listener = &DispatchedListener{}

func NewDispatchedListener(addrFunc func() net.Addr) *DispatchedListener {
	return &DispatchedListener{
		addrFunc: addrFunc,

		connQueue:  make(chan net.Conn),
		errorQueue: make(chan error),
	}
}

func (l *DispatchedListener) RunCallbackDispatcher(orig net.Listener, callback func(net.Conn) (net.Conn, error)) {
	for {
		conn, err := orig.Accept()
		if err != nil {
			// return error to next accept call
			l.SubmitErr(err) // okay to not check error because it's fine if it's closed
			return
		}

		// must use channels and goroutines so we don't stall other conns for long running callback
		go func() {
			if l.Closed() {
				// don't run callback if closed
				return
			}

			newConn, err := callback(conn)
			if err != nil {
				conn.Close()
				l.SubmitErr(err)
				return
			}

			if newConn != nil {
				// passthrough
				err := l.SubmitConn(newConn)
				if err != nil {
					// l closed while running callback
					conn.Close()
					return
				}
			}
			// swallow conn otherwise. callback is responsible for closing it
		}()
	}
}

func (l *DispatchedListener) Accept() (net.Conn, error) {
	if l.closed.Load() {
		return nil, net.ErrClosed
	}

	select {
	case conn, ok := <-l.connQueue:
		if !ok {
			return nil, net.ErrClosed
		}
		return conn, nil
	case err, ok := <-l.errorQueue:
		if !ok {
			return nil, net.ErrClosed
		}
		return nil, err
	}
}

func (l *DispatchedListener) Close() error {
	if l.closed.Swap(true) {
		return net.ErrClosed
	}

	close(l.connQueue)
	close(l.errorQueue)
	return nil
}

func (l *DispatchedListener) Closed() bool {
	return l.closed.Load()
}

func (l *DispatchedListener) Addr() net.Addr {
	return l.addrFunc()
}

func (l *DispatchedListener) SubmitConn(conn net.Conn) error {
	if l.closed.Load() {
		return net.ErrClosed
	}
	l.connQueue <- conn
	return nil
}

func (l *DispatchedListener) SubmitErr(err error) error {
	if l.closed.Load() {
		return net.ErrClosed
	}
	l.errorQueue <- err
	return nil
}
