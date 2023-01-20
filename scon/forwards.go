package main

type HostForwardSpec struct {
	Port int
}

func (m *ConManager) addForward(spec HostForwardSpec) {
	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	newCount := m.forwards[spec]++
	if newCount == 1 {
		
}
