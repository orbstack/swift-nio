package nft

import (
	"fmt"

	"github.com/google/nftables"
)

func WithChain(family nftables.TableFamily, table string, chain string, fn func(*nftables.Conn, *nftables.Table, *nftables.Chain) error) error {
	return WithTable(family, table, func(conn *nftables.Conn, table *nftables.Table) error {
		chain, err := conn.ListChain(table, chain)
		if err != nil {
			return fmt.Errorf("get chain: %w", err)
		}

		return fn(conn, table, chain)
	})
}

func FlushChain(family nftables.TableFamily, table string, chain string) error {
	return WithChain(family, table, chain, func(conn *nftables.Conn, table *nftables.Table, chain *nftables.Chain) error {
		conn.FlushChain(chain)

		err := conn.Flush()
		if err != nil {
			return fmt.Errorf("flush chain: %w", err)
		}

		return nil
	})
}
