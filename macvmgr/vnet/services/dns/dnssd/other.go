//go:build !darwin

package dnssd

import (
	"errors"
)

func QueryRecursive(name string, rtype uint16) ([]QueryAnswer, error) {
	return nil, errors.New("not implemented")
}
