//go:build !darwin

package drmcore

import (
	"errors"
)

func SaveRefreshToken(refreshToken string) error {
	return errors.New("unsupported")
}

func ReadKeychainState() ([]byte, error) {
	return nil, errors.New("unsupported")
}

func SetKeychainState(data []byte) error {
	return errors.New("unsupported")
}
