package main

type HostForwardSpec struct {
	Port int
}

func (m *ConManager) addForward(spec HostForwardSpec) {
	m.forwardsMu.Lock()
	defer m.forwardsMu.Unlock()

	m.forwards[spec]++
	if m.forwards[spec] == 1 {
		// new

		// report to host
		

		// TODO: start proxy
	}
}
