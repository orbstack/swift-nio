//go:build !darwin

package vmconfig

func validateAPFS(dataDir string) error {
	return nil
}
