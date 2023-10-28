package agent

import (
	"net"
	"sync/atomic"
)

type DispatchedListener struct {
	orig     net.Listener
	callback func(net.Conn) (bool, error)

	closed atomic.Bool

	passthruConns  chan net.Conn
	passthruErrors chan error
}

// interface conformance
var _ net.Listener = &DispatchedListener{}

func NewDispatchedListener(orig net.Listener, callback func(net.Conn) (bool, error)) *DispatchedListener {
	return &DispatchedListener{
		orig:     orig,
		callback: callback,
		// unbuffered in order to preserve listener backlog pressure
		passthruConns:  make(chan net.Conn),
		passthruErrors: make(chan error),
	}
}

func (l *DispatchedListener) Run() {
	for {
		conn, err := l.orig.Accept()
		if err != nil {
			// return error to next accept call
			l.passthruErrors <- err
			return
		}

		// must use channels and goroutines because callback can take up to 500ms and stall all other conns
		go func() {
			cont, err := l.callback(conn)
			if err != nil {
				conn.Close()
				l.passthruErrors <- err
				return
			}

			if cont {
				// keep conn alive for Accept
				l.passthruConns <- conn
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
	case conn, ok := <-l.passthruConns:
		if !ok {
			return nil, net.ErrClosed
		}
		return conn, nil
	case err, ok := <-l.passthruErrors:
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

	close(l.passthruConns)
	close(l.passthruErrors)
	return l.orig.Close()
}

func (l *DispatchedListener) Addr() net.Addr {
	return l.orig.Addr()
}
