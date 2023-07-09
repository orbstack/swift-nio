package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/orbstack/macvirt/scon/securefs"
)

const (
	certPrefix = "orb-extra-"
)

func WriteCaCerts(fs *securefs.FS, dir string, certs []string) error {
	certFiles, err := fs.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read certs dir: %w", err)
	}
	for _, f := range certFiles {
		if strings.HasPrefix(f.Name(), certPrefix) {
			err = fs.Remove(dir + "/" + f.Name())
			if err != nil {
				return fmt.Errorf("remove cert: %w", err)
			}
		}
	}
	// write new certs
	for _, cert := range certs {
		// hash the cert
		h := sha256.New()
		_, _ = h.Write([]byte(cert))
		hash := hex.EncodeToString(h.Sum(nil))[:8]

		// write cert
		certPath := dir + "/" + certPrefix + hash + ".crt"
		err = fs.WriteFile(certPath, []byte(cert), 0644)
		if err != nil {
			return fmt.Errorf("write cert: %w", err)
		}
	}

	return nil
}
