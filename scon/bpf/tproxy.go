package bpf

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/cilium/ebpf/link"
	"github.com/orbstack/macvirt/scon/util"
)

type Tproxy struct {
	objs  *tproxyObjects
	links []*link.NetNsLink
}

const (
	cMAX_PORTS = 2

	cSOCKET_KEY4    = 0
	cSOCKET_KEY6    = 1
	cSOCKET_KEY_MAX = 2
)

func ipv4AddrToUint32(addr netip.Addr) uint32 {
	a := addr.As4()
	return binary.NativeEndian.Uint32(a[:])
}

func ipv4BitsToMaskUint32(bits int) uint32 {
	return util.SwapNetHost32(^uint32(0) << (32 - bits))
}

func ipv6AddrToUint32Array(addr netip.Addr) [4]uint32 {
	a := addr.As16()
	return [4]uint32{
		binary.NativeEndian.Uint32(a[0:]),
		binary.NativeEndian.Uint32(a[4:]),
		binary.NativeEndian.Uint32(a[8:]),
		binary.NativeEndian.Uint32(a[12:]),
	}
}

func ipv6BitsToMaskUint32Array(bits int) [4]uint32 {
	return [4]uint32{
		util.SwapNetHost32(^uint32(0) << max(32-bits, 0)),
		util.SwapNetHost32(^uint32(0) << max(64-bits, 0)),
		util.SwapNetHost32(^uint32(0) << max(96-bits, 0)),
		util.SwapNetHost32(^uint32(0) << max(128-bits, 0)),
	}
}

// 0 port means any port, invalid netip.Prefix means disabled
func NewTproxy(subnet4 netip.Prefix, subnet6 netip.Prefix, ports []uint16) (*Tproxy, error) {
	if len(ports) > cMAX_PORTS {
		return nil, fmt.Errorf("too many ports: %d", len(ports))
	}
	if len(ports) < cMAX_PORTS {
		// pad to match bpf port array size
		paddedPorts := make([]uint16, cMAX_PORTS)
		copy(paddedPorts, ports)
		ports = paddedPorts
	}

	spec, err := loadTproxy()
	if err != nil {
		return nil, fmt.Errorf("load tproxy spec: %w", err)
	}

	// these are already big endian so we do native endian to keep them that way. if we specified big endian it would make it into host order
	subnet4Enabled := subnet4.IsValid()
	subnet4IP := ipv4AddrToUint32(subnet4.Addr())
	subnet4Mask := ipv4BitsToMaskUint32(subnet4.Bits())

	subnet6Enabled := subnet6.IsValid()
	subnet6IP := ipv6AddrToUint32Array(subnet6.Addr())
	subnet6Mask := ipv6BitsToMaskUint32Array(subnet6.Bits())

	err = spec.Variables["config_tproxy_subnet4_enabled"].Set(subnet4Enabled)
	if err != nil {
		return nil, fmt.Errorf("set subnet4 enabled: %w", err)
	}
	err = spec.Variables["config_tproxy_subnet4_ip"].Set(subnet4IP)
	if err != nil {
		return nil, fmt.Errorf("set subnet4 ip: %w", err)
	}
	err = spec.Variables["config_tproxy_subnet4_mask"].Set(subnet4Mask)
	if err != nil {
		return nil, fmt.Errorf("set subnet4 mask: %w", err)
	}
	err = spec.Variables["config_tproxy_subnet6_enabled"].Set(subnet6Enabled)
	if err != nil {
		return nil, fmt.Errorf("set subnet6 enabled: %w", err)
	}
	err = spec.Variables["config_tproxy_subnet6_ip"].Set(subnet6IP)
	if err != nil {
		return nil, fmt.Errorf("set subnet6 ip: %w", err)
	}
	err = spec.Variables["config_tproxy_subnet6_mask"].Set(subnet6Mask)
	if err != nil {
		return nil, fmt.Errorf("set subnet6 mask: %w", err)
	}
	err = spec.Variables["config_tproxy_ports"].Set(ports)
	if err != nil {
		return nil, fmt.Errorf("set ports: %w", err)
	}

	tproxyObjs := &tproxyObjects{}
	err = spec.LoadAndAssign(tproxyObjs, nil)
	if err != nil {
		return nil, fmt.Errorf("load tproxy: %w", err)
	}

	return &Tproxy{
		objs:  tproxyObjs,
		links: make([]*link.NetNsLink, 0),
	}, nil
}

func (t *Tproxy) AttachNetNs(nsFd int) error {
	l, err := link.AttachNetNs(nsFd, t.objs.TproxySkLookup)
	if err != nil {
		return fmt.Errorf("attach tproxy: %w", err)
	}

	t.links = append(t.links, l)
	return nil
}

func (t *Tproxy) SetSock4(portIndex int, fd uint64) error {
	return t.objs.TproxySocket.Put(uint32(portIndex*cSOCKET_KEY_MAX+cSOCKET_KEY4), fd)
}

func (t *Tproxy) SetSock6(portIndex int, fd uint64) error {
	return t.objs.TproxySocket.Put(uint32(portIndex*cSOCKET_KEY_MAX+cSOCKET_KEY6), fd)
}
