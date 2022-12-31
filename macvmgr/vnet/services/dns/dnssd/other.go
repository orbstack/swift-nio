//go:build !darwin

package dnssd

func QueryRecursive(name string, rtype uint16) ([]QueryAnswer, error) {
	return nil, errors.New("not implemented")
}
