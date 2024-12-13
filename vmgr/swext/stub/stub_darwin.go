//go:build darwin

package swext_stub

import "C"

// provided by libvirtue in vmgr
//
//export rsvm_network_write_packet
func rsvm_network_write_packet() {}
