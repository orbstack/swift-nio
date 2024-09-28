package bpf

import (
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/cilium/ebpf/link"
	"github.com/orbstack/macvirt/scon/util"
	"golang.org/x/sys/unix"
)

type Tproxy struct {
	objs *tproxyObjects
	link *link.NetNsLink
}

const TPROXY_SOCKET_KEY4 uint32 = 0
const TPROXY_SOCKET_KEY6 uint32 = 1

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

func NewTproxy(subnet4 netip.Prefix, subnet6 netip.Prefix, port uint16) (*Tproxy, error) {
	spec, err := loadTproxy()
	if err != nil {
		return nil, fmt.Errorf("load tproxy spec: %w", err)
	}

	// these are already big endian so we do native endian to keep them that way. if we specified big endian it would make it into host order
	subnet4Enabled := subnet4 != netip.Prefix{}
	subnet4Ip := ipv4AddrToUint32(subnet4.Addr())
	subnet4Mask := ipv4BitsToMaskUint32(subnet4.Bits())

	subnet6Enabled := subnet6 != netip.Prefix{}
	subnet6Ip := ipv6AddrToUint32Array(subnet6.Addr())
	subnet6Mask := ipv6BitsToMaskUint32Array(subnet6.Bits())

	err = spec.RewriteConstants(map[string]any{
		"config_tproxy_port":            port,
		"config_tproxy_subnet4_enabled": subnet4Enabled,
		"config_tproxy_subnet4_ip":      subnet4Ip,
		"config_tproxy_subnet4_mask":    subnet4Mask,
		"config_tproxy_socket_key4":     TPROXY_SOCKET_KEY4,
		"config_tproxy_subnet6_enabled": subnet6Enabled,
		"config_tproxy_subnet6_ip":      subnet6Ip,
		"config_tproxy_subnet6_mask":    subnet6Mask,
		"config_tproxy_socket_key6":     TPROXY_SOCKET_KEY6,
	})
	if err != nil {
		return nil, fmt.Errorf("rewrite constants: %w", err)
	}

	tproxyObjs := &tproxyObjects{}
	err = spec.LoadAndAssign(tproxyObjs, nil)
	if err != nil {
		return nil, fmt.Errorf("load tproxy: %w", err)
	}

	nsFd, err := unix.Open("/proc/thread-self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open netns: %w", err)
	}
	defer unix.Close(nsFd)

	l, err := link.AttachNetNs(nsFd, tproxyObjs.TproxySkLookup)
	if err != nil {
		return nil, fmt.Errorf("attach tproxy: %w", err)
	}

	return &Tproxy{
		objs: tproxyObjs,
		link: l,
	}, nil
}

func (t *Tproxy) SetSock4(fd uint64) error {
	return t.objs.TproxySocket.Put(TPROXY_SOCKET_KEY4, fd)
}

func (t *Tproxy) SetSock6(fd uint64) error {
	return t.objs.TproxySocket.Put(TPROXY_SOCKET_KEY6, fd)
}
