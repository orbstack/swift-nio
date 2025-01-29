package util

import (
	"fmt"
	"strings"
	"sync"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

type MutationHoldManager struct {
	mu    syncx.Mutex
	holds map[string]int

	mutationWg sync.WaitGroup
}

func NewMutationHoldManager() *MutationHoldManager {
	return &MutationHoldManager{
		holds: make(map[string]int),
	}
}

func (m *MutationHoldManager) addHold(op string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// wait for ongoing mutations to complete
	m.mutationWg.Wait()

	m.holds[op]++
}

func (m *MutationHoldManager) releaseHold(op string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.holds[op]--
	if m.holds[op] == 0 {
		delete(m.holds, op)
	}
}

func (m *MutationHoldManager) WithHold(op string, fn func() error) error {
	m.addHold(op)
	defer m.releaseHold(op)

	return fn()
}

func (m *MutationHoldManager) BeginMutation(op string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.holds) > 0 {
		holds := make([]string, 0, len(m.holds))
		for hold := range m.holds {
			holds = append(holds, hold)
		}
		return fmt.Errorf("cannot %s: operations are in progress: %s", op, strings.Join(holds, ", "))
	}

	// start a new mutation
	m.mutationWg.Add(1)
	return nil
}

func (m *MutationHoldManager) EndMutation() {
	m.mutationWg.Done()
}

func (m *MutationHoldManager) WithMutation(op string, fn func() error) error {
	err := m.BeginMutation(op)
	if err != nil {
		return err
	}
	defer m.EndMutation()

	return fn()
}
