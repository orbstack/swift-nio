//go:build !arm64

package arch

func CreateRosettaDevice() (*RosettaResult, error) {
	return nil, nil
}
