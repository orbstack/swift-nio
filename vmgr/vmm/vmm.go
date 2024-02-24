package vmm

import (
	"net"
	"os"
)

type VmmType int

const (
	VmmVzf VmmType = iota
	VmmOrbvmm
)

type VmParams struct {
}

type VMM interface {
	CreateVM(c *VmParams, retainFiles []*os.File) (Machine, bool, error)
}

type Machine interface {
	Start() error
	ForceStop() error
	RequestStop() error
	Pause() error
	Resume() error
	ConnectVsock(port uint32) (net.Conn, error)
	Close() error
}
