package vmm

import (
	"net"
	"os"
)

type MachineState int

// matches Swift enum
const (
	MachineStateStopped MachineState = iota
	MachineStateRunning
	MachineStatePaused
	MachineStateError
	MachineStateStarting
	MachineStatePausing
	MachineStateResuming
	// (VZF) macOS 12
	MachineStateStopping
)

type ConsoleSpec struct {
	Earlycon bool `json:"earlycon"`
	ReadFd   int  `json:"readFd"`
	WriteFd  int  `json:"writeFd"`
}

type NfsInfo struct {
	DirDev         int32  `json:"dirDev"`
	DirInode       uint64 `json:"dirInode"`
	DirName        string `json:"dirName"`
	ParentDirDev   int32  `json:"parentDirDev"`
	ParentDirInode uint64 `json:"parentDirInode"`
	EmptyDirInode  uint64 `json:"emptyDirInode"`
}

type VzSpec struct {
	Cpus             int          `json:"cpus"`
	Memory           uint64       `json:"memory"`
	Kernel           string       `json:"kernel"`
	Cmdline          string       `json:"cmdline"`
	Initrd           string       `json:"initrd,omitempty"`
	Console          *ConsoleSpec `json:"console"`
	Mtu              int          `json:"mtu"`
	MacAddressPrefix string       `json:"macAddressPrefix"`
	NetworkNat       bool         `json:"networkNat"`
	NetworkFds       []int        `json:"networkFds"`
	Rng              bool         `json:"rng"`
	DiskRootfs       string       `json:"diskRootfs,omitempty"`
	DiskData         string       `json:"diskData,omitempty"`
	DiskSwap         string       `json:"diskSwap,omitempty"`
	Balloon          bool         `json:"balloon"`
	Vsock            bool         `json:"vsock"`
	Virtiofs         bool         `json:"virtiofs"`
	Rosetta          bool         `json:"rosetta"`
	Sound            bool         `json:"sound"`
	Rconfig          []byte       `json:"rconfig,omitempty"`

	// for loop prevention
	NfsInfo *NfsInfo `json:"nfsInfo"`
}

type Monitor interface {
	NewMachine(c *VzSpec, retainFiles []*os.File) (Machine, error)
	NetworkMTU() int
}

type Machine interface {
	Start() error
	ForceStop() error
	RequestStop() error
	Pause() error
	Resume() error
	ConnectVsock(port uint32) (net.Conn, error)
	StateChan() <-chan MachineState
	Close() error
}
