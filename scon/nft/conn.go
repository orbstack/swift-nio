package nft

import (
	"fmt"

	"github.com/google/nftables"
)

const FamilyInet = nftables.TableFamilyINet
const FamilyBridge = nftables.TableFamilyBridge

func WithConn(f func(conn *nftables.Conn) error) error {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return fmt.Errorf("new: %w", err)
	}
	defer conn.CloseLasting()

	err = f(conn)
	if err != nil {
		return fmt.Errorf("callback: %w", err)
	}

	err = conn.Flush()
	if err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	return nil
}

func WithTable(family nftables.TableFamily, tableName string, f func(conn *nftables.Conn, table *nftables.Table) error) error {
	return WithConn(func(conn *nftables.Conn) error {
		table, err := conn.ListTableOfFamily(tableName, family)
		if err != nil {
			return err
		}

		return f(conn, table)
	})
}

func WithSet(family nftables.TableFamily, tableName string, setName string, f func(conn *nftables.Conn, set *nftables.Set) error) error {
	return WithTable(family, tableName, func(conn *nftables.Conn, table *nftables.Table) error {
		set, err := conn.GetSetByName(table, setName)
		if err != nil {
			return err
		}

		return f(conn, set)
	})
}
