package agent

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
)

func loadRoot() (*x509.Certificate, crypto.PrivateKey, error) {
	// TODO
	pemData, err := os.ReadFile("/Users/dragon/Library/Application Support/mkcert/rootCA.pem")
	if err != nil {
		return nil, nil, err
	}
	pemBlock, _ := pem.Decode(pemData)
	if pemBlock == nil {
		return nil, nil, fmt.Errorf("decode cert PEM: failed")
	}
	cert, err := x509.ParseCertificate(pemBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse cert: %w", err)
	}

	keyData, err := os.ReadFile("/Users/dragon/Library/Application Support/mkcert/rootCA-key.pem")
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyData)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("decode key PEM: failed")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse key: %w", err)
	}

	return cert, key, nil
}

func (t *tlsProxy) generateCert(host string) (*tls.Certificate, error) {
	// ECDSA is fast to generate
	sk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	pk := sk.Public()

	serial, err := genSerial()
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{appid.UserAppName},
			// CommonName not needed - we only use SAN
		},
		DNSNames: []string{host},

		NotBefore: time.Now(),
		// from mkcert: 2yr, 3mo due to macOS 825-day limit: https://support.apple.com/en-us/HT210176
		NotAfter: time.Now().AddDate(2, 3, 0),

		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	cert, err := x509.CreateCertificate(rand.Reader, template, t.rootCert, pk, t.rootKey)
	if err != nil {
		return nil, fmt.Errorf("create cert: %w", err)
	}

	// skip tls.X509KeyPair and pass data directly for efficiency and simplicity
	return &tls.Certificate{
		// DER block
		// no root CA in chain:
		// - if we pass it, user still needs to install cert in system and mark as trusted, or verify fails
		// - if we don't pass it, it's no different
		// TODO test with curl -k and chrome when cert is not present in system store
		Certificate: [][]byte{cert},
		PrivateKey:  sk,
	}, nil
}

// from mkcert
func genSerial() (*big.Int, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, serialNumberLimit)
}
