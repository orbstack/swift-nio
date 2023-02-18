//go:build !arm64

package arch

import "github.com/Code-Hex/vz/v3"

func CreateRosettaDevice() (*RosettaResult, error) {
	return nil, nil
}
