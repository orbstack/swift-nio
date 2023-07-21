package registry

import (
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/orbstack/macvirt/scon/util/netx"
)

type LocalTCPRegistry struct {
	mu    sync.Mutex
	ports map[uint16]*localTCPListener
}

type localTCPListener struct {
	// to take up the port forward
	net.Listener
	ch       chan *net.TCPConn
	registry *LocalTCPRegistry
	closed   atomic.Bool
}

func (l *localTCPListener) Accept() (net.Conn, error) {
	// accept from the channel
	conn := <-l.ch
	if conn == nil {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *localTCPListener) Close() error {
	if !l.closed.CompareAndSwap(false, true) {
		return nil
	}

	// close the channel
	close(l.ch)
	l.registry.mu.Lock()
	delete(l.registry.ports, uint16(l.Addr().(*net.TCPAddr).Port))
	l.registry.mu.Unlock()
	return l.Listener.Close()
}

func NewLocalTCPRegistry() *LocalTCPRegistry {
	return &LocalTCPRegistry{
		ports: make(map[uint16]*localTCPListener),
	}
}

func (r *LocalTCPRegistry) Listen(port uint16) (net.Listener, error) {
	listener, err := netx.Listen("tcp", "127.0.0.1:"+strconv.Itoa(int(port)))
	if err != nil {
		return nil, err
	}

	ch := make(chan *net.TCPConn)
	localListener := &localTCPListener{
		Listener: listener,
		ch:       ch,
		registry: r,
	}
	r.mu.Lock()
	r.ports[port] = localListener
	r.mu.Unlock()

	return localListener, nil
}

func (r *LocalTCPRegistry) TakeConn(port uint16, conn net.Conn) bool {
	r.mu.Lock()
	listener, ok := r.ports[port]
	r.mu.Unlock()
	if !ok {
		return false
	}

	// send it to the channel
	netx.DisableKeepalive(conn)
	listener.ch <- conn.(*net.TCPConn)
	return true
}
