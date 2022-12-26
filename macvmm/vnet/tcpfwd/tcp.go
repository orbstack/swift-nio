package tcpfwd

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/kdrag0n/macvirt/macvmm/vnet/netutil"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// mitigation for hangs with 10 concurrent dials
const (
	tcpConnectTimeout = 15 * time.Second
)

func NewTcpForwarder(s *stack.Stack, natTable map[tcpip.Address]tcpip.Address, natLock *sync.RWMutex) *tcp.Forwarder {
	return tcp.NewForwarder(s, 0, 10, func(r *tcp.ForwarderRequest) {
		// Workaround for NFS panic
		defer func() {
			if err := recover(); err != nil {
				log.Errorf("tcpfwd: panic: %v", err)
			}
		}()

		localAddress := r.ID().LocalAddress
		fmt.Println("new tcp conn req", localAddress)
		if !netutil.ShouldProxy(localAddress) {
			r.Complete(false)
			return
		}

		natLock.RLock()
		if replaced, ok := natTable[localAddress]; ok {
			localAddress = replaced
		}
		natLock.RUnlock()
		extAddr := net.JoinHostPort(localAddress.String(), strconv.Itoa(int(r.ID().LocalPort)))

		fmt.Println("dialing", extAddr)
		extConn, err := net.DialTimeout("tcp", extAddr, tcpConnectTimeout)
		fmt.Println("done dialing", extAddr, err)
		if err != nil {
			log.Errorf("net.Dial() %v = %v", extAddr, err)
			// if connection refused
			if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, unix.ECONNRESET) {
				// send RST
				r.Complete(true)
			} else if errors.Is(err, unix.EHOSTUNREACH) || errors.Is(err, unix.EHOSTDOWN) || errors.Is(err, unix.ENETUNREACH) {
				// TODO: icmp response
				r.Complete(false)
			} else if errors.Is(err, unix.ETIMEDOUT) {
				r.Complete(false)
			} else {
				// unknown
				r.Complete(false)
			}
			return
		}
		defer extConn.Close()

		var wq waiter.Queue
		fmt.Printf("creating endpoint %v\n", extAddr)
		ep, tcpErr := r.CreateEndpoint(&wq)
		r.Complete(false)
		if tcpErr != nil {
			// Maybe VM abandoned the connection already, nothing to do
			log.Errorf("r.CreateEndpoint() [%v] = %v", extAddr, tcpErr)
			return
		}

		virtConn := gonet.NewTCPConn(&wq, ep)
		defer virtConn.Close()

		fmt.Println("pumping", extAddr)
		pump2(virtConn, extConn)
	})
}
