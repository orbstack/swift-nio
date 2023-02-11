package sjwt

import (
	"crypto/ed25519"
	_ "embed"

	"golang.org/x/crypto/ssh"
)

const (
	pkProdStr = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDuwnPcAbnT0Hdku/Z+PMf4oeeIkQy9S3VCVRyhiihOl"
	pkDevStr  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIO/L+dj66P4+a/T7EoNjA7zvp/G8M4iRUFhA15hYgQsI"
)

var (
	//go:embed jwt-prod.pub.bin
	pkProdBin []byte
	//go:embed jwt-dev.pub.bin
	pkDevBin []byte
)

func parsePkStr(pkStr string) (ed25519.PublicKey, error) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pkStr))
	if err != nil {
		return nil, err
	}
	return pk.(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey), nil
}

func parsePkBin(pkBin []byte) (ed25519.PublicKey, error) {
	pk, err := ssh.ParsePublicKey(pkBin)
	if err != nil {
		return nil, err
	}
	return pk.(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey), nil
}
