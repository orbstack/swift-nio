package vclient

import (
	"crypto/rand"
	"encoding/base32"
)

var (
	instanceToken = genToken()
)

func genToken() string {
	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}

	// to base32
	b32str := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return b32str
}

func GetCurrentToken() string {
	return instanceToken
}
