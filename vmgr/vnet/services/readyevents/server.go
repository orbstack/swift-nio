package readyevents

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/services/newtwork"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type ReadyHandler func(string)

type Service struct {
	mutex         sync.Mutex
	serviceStates map[string]*serviceState
}

type serviceState struct {
	// Synchronized by `ReadyEventsService.mutex`
	isReady  bool
	handlers []ReadyHandler

	// Not synchronized by `ReadyEventsService.mutex`. In fact, don't hold that
	// lock while waiting on this one.
	ready sync.RWMutex
}

func ListenReadyEventsService(stack *stack.Stack, addr tcpip.Address) (*Service, error) {
	listener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: addr,
		Port: ports.SecureSvcReadyEvents,
	}, ipv4.ProtocolNumber)

	if err != nil {
		return nil, err
	}

	s := &Service{
		serviceStates: make(map[string]*serviceState),
	}

	go newtwork.ProcessAccepts(listener, func(c net.Conn) {
		buffer := make([]byte, 1024)
		var accum []byte

		for {
			read, err := c.Read(buffer)
			if err != nil {
				// If this is an EOF, we're done.
				if errors.Is(err, io.EOF) {
					if len(accum) > 0 {
						logrus.Errorf("ReadyEvents socket did not end packet with newline")
					}
				} else {
					// Otherwise, something bad happened.
					logrus.Errorf("Failed to process ready event socket: %e", err)
				}

				return
			}

			for _, b := range buffer[:read] {
				if b != (byte)('\n') {
					accum = append(accum, b)
				} else {
					logrus.WithField("service", string(accum)).Info("service is ready")
					s.MarkReady(string(accum))
					accum = nil
				}
			}
		}
	})

	return s, nil
}

func (s *Service) MarkReady(name string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	state := s.serviceStates[name]

	if state == nil {
		// This is a new service. Let the ready mutex be acquired immediately and mark the
		// task as ready so new handlers don't get added to it. Everything else can be zero
		// initialized.
		s.serviceStates[name] = &serviceState{isReady: true}
	} else if !state.isReady {
		// Mark the service as ready so no new handlers can be added.
		state.isReady = true

		// Unblock readers waiting for the service to be ready
		state.ready.Unlock()

		// Call ready callbacks
		for _, handler := range state.handlers {
			handler(name)
		}
	}
}

func (s *Service) getServiceStateForWaitingLocked(name string) *serviceState {
	state := s.serviceStates[name]

	// If the service doesn't exist, create it.
	if state == nil {
		state = &serviceState{}

		// This will be unlocked once the service is ready.
		state.ready.Lock()

		s.serviceStates[name] = state
	}

	return state
}

func (s *Service) WaitForReady(name string) {
	state := func() *serviceState {
		s.mutex.Lock()
		defer s.mutex.Unlock()
		return s.getServiceStateForWaitingLocked(name)
	}()

	// Wait for a read lock to be acquired and then release it to avoid exhausting the reader counter.
	state.ready.RLock()
	state.ready.RUnlock()
}

func (s *Service) PushReadyHandler(name string, handler ReadyHandler) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	state := s.getServiceStateForWaitingLocked(name)
	if state.isReady {
		handler(name)
	} else {
		state.handlers = append(state.handlers, handler)
	}
}
