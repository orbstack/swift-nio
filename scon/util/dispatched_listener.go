package util

import (
	"net"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
)

type DispatchedListener struct {
	closed     atomic.Bool
	closedChan chan struct{}
	closeOnce  sync.Once

	addrFunc func() net.Addr

	connQueue  chan net.Conn
	errorQueue chan error
}

// check interface conformance
var _ net.Listener = &DispatchedListener{}

func NewDispatchedListener(addrFunc func() net.Addr) *DispatchedListener {
	return &DispatchedListener{
		closedChan: make(chan struct{}),

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
				logrus.WithError(err).Error("dispatched listener: callback failed")
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
	select {
	case conn := <-l.connQueue:
		return conn, nil
	case err := <-l.errorQueue:
		return nil, err
	case <-l.closedChan:
		return nil, net.ErrClosed
	}
}

func (l *DispatchedListener) Close() error {
	didClose := false
	l.closeOnce.Do(func() {
		close(l.connQueue)
		close(l.errorQueue)
		l.closed.Store(true)
		didClose = true
	})

	if !didClose {
		return net.ErrClosed
	} else {
		return nil
	}
}

func (l *DispatchedListener) Closed() bool {
	return l.closed.Load()
}

func (l *DispatchedListener) Addr() net.Addr {
	return l.addrFunc()
}

func (l *DispatchedListener) SubmitConn(conn net.Conn) error {
	select {
	case l.connQueue <- conn:
		return nil
	case <-l.closedChan:
		return net.ErrClosed
	}
}

func (l *DispatchedListener) SubmitErr(err error) error {
	select {
	case l.errorQueue <- err:
		return nil
	case <-l.closedChan:
		return net.ErrClosed
	}
}
