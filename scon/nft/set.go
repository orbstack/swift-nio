package nft

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/google/nftables"
	"golang.org/x/sys/unix"
)

type NftValue interface {
	SizeNft() int
	MarshalNft() []byte
}

type IPv4Addr [4]byte

func (v IPv4Addr) SizeNft() int {
	return 4
}

func (v IPv4Addr) MarshalNft() []byte {
	return v[:]
}

type IPv6Addr [16]byte

func (v IPv6Addr) SizeNft() int {
	return 16
}

func (v IPv6Addr) MarshalNft() []byte {
	return v[:]
}

type InetService uint16

func (v InetService) SizeNft() int {
	return 2
}

func (v InetService) MarshalNft() []byte {
	return []byte{byte(v >> 8), byte(v)}
}

type IfName string

func (v IfName) SizeNft() int {
	return unix.IFNAMSIZ
}

func (v IfName) MarshalNft() []byte {
	var buf [unix.IFNAMSIZ]byte
	copy(buf[:], v)
	return buf[:]
}

type nftValueConcat []NftValue

func (v nftValueConcat) SizeNft() int {
	size := 0
	for _, val := range v {
		size += val.SizeNft()
	}

	// pad to register size (4 bytes)
	// https://git.netfilter.org/nftables/tree/src/datatype.c?id=488356b895024d0944b20feb1f930558726e0877#n1162
	if size%4 != 0 {
		size += 4 - (size % 4)
	}

	return size
}

func (v nftValueConcat) MarshalNft() []byte {
	buf := make([]byte, v.SizeNft())
	offset := 0
	for _, val := range v {
		copy(buf[offset:], val.MarshalNft())
		offset += val.SizeNft()
	}
	return buf
}

func Concat(vals ...NftValue) nftValueConcat {
	return nftValueConcat(vals)
}

func IPAddr(addr netip.Addr) NftValue {
	if addr.Is4() {
		return IPv4Addr(addr.As4())
	}
	return IPv6Addr(addr.As16())
}

func IP(ip net.IP) NftValue {
	ip4 := ip.To4()
	if ip4 != nil {
		return IPv4Addr(ip4)
	}
	return IPv6Addr(ip.To16())
}

func SetAdd(conn *nftables.Conn, set *nftables.Set, key NftValue) error {
	err := conn.SetAddElements(set, []nftables.SetElement{
		{
			Key: key.MarshalNft(),
		},
	})
	if err != nil {
		return err
	}

	// return error immediately, not after batch
	err = conn.Flush()
	if err != nil {
		return fmt.Errorf("add to set: %w", err)
	}

	return nil
}

func SetDelete(conn *nftables.Conn, set *nftables.Set, key NftValue) error {
	err := conn.SetDeleteElements(set, []nftables.SetElement{
		{
			Key: key.MarshalNft(),
		},
	})
	if err != nil {
		return err
	}

	// return error immediately, not after batch
	err = conn.Flush()
	if err != nil {
		return fmt.Errorf("delete from set: %w", err)
	}

	return nil
}

func SetAddByName(conn *nftables.Conn, table *nftables.Table, setName string, key NftValue) error {
	set, err := conn.GetSetByName(table, setName)
	if err != nil {
		return fmt.Errorf("get set: %w", err)
	}
	return SetAdd(conn, set, key)
}

func SetDeleteByName(conn *nftables.Conn, table *nftables.Table, setName string, key NftValue) error {
	set, err := conn.GetSetByName(table, setName)
	if err != nil {
		return fmt.Errorf("get set: %w", err)
	}
	return SetDelete(conn, set, key)
}

func MapAdd(conn *nftables.Conn, set *nftables.Set, key NftValue, val NftValue) error {
	err := conn.SetAddElements(set, []nftables.SetElement{
		{
			Key: key.MarshalNft(),
			Val: val.MarshalNft(),
		},
	})
	if err != nil {
		return err
	}

	// return error immediately, not after batch
	err = conn.Flush()
	if err != nil {
		return fmt.Errorf("add to map: %w", err)
	}

	return nil
}

func MapDelete(conn *nftables.Conn, set *nftables.Set, key NftValue) error {
	err := conn.SetDeleteElements(set, []nftables.SetElement{
		{
			Key: key.MarshalNft(),
		},
	})
	if err != nil {
		return err
	}

	// return error immediately, not after batch
	err = conn.Flush()
	if err != nil {
		return fmt.Errorf("delete from map: %w", err)
	}

	return nil
}

func MapAddByName(conn *nftables.Conn, table *nftables.Table, setName string, key NftValue, val NftValue) error {
	set, err := conn.GetSetByName(table, setName)
	if err != nil {
		return fmt.Errorf("get set: %w", err)
	}
	return MapAdd(conn, set, key, val)
}

func MapDeleteByName(conn *nftables.Conn, table *nftables.Table, setName string, key NftValue) error {
	set, err := conn.GetSetByName(table, setName)
	if err != nil {
		return fmt.Errorf("get set: %w", err)
	}
	return MapDelete(conn, set, key)
}
