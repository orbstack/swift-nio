package readyevents

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/services/newtwork"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type ReadyHandler func(string)

type Service struct {
	mu            syncx.Mutex
	serviceStates map[string]*serviceState
}

type serviceState struct {
	// Synchronized by `ReadyEventsService.mutex`
	isReady  bool
	handlers []ReadyHandler

	// Not synchronized by `ReadyEventsService.mutex`. In fact, don't hold that
	// lock while waiting on this one.
	ready sync.WaitGroup
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
	s.mu.Lock()
	defer s.mu.Unlock()

	if state, ok := s.serviceStates[name]; ok {
		if !state.isReady {
			// Mark the service as ready so no new handlers can be added.
			state.isReady = true

			// Unblock readers waiting for the service to be ready
			state.ready.Done()

			// Call ready callbacks
			for _, handler := range state.handlers {
				handler(name)
			}
			state.handlers = nil
		}
	} else {
		// This is a new service. Let the ready mutex be acquired immediately and mark the
		// task as ready so new handlers don't get added to it. Everything else can be zero
		// initialized.
		s.serviceStates[name] = &serviceState{isReady: true}
	}
}

func (s *Service) getServiceStateForWaitingLocked(name string) *serviceState {
	if state, ok := s.serviceStates[name]; ok {
		return state
	}

	// If the service doesn't exist, create it.
	state := &serviceState{}
	state.ready.Add(1)
	s.serviceStates[name] = state
	return state
}

func (s *Service) WaitForReady(name string) {
	s.mu.Lock()
	state := s.getServiceStateForWaitingLocked(name)
	s.mu.Unlock()

	state.ready.Wait()
}

func (s *Service) PushReadyHandler(name string, handler ReadyHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.getServiceStateForWaitingLocked(name)
	if state.isReady {
		handler(name)
	} else {
		state.handlers = append(state.handlers, handler)
	}
}
