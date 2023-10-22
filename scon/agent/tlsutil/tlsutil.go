package tlsutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
)

func LoadRoot(pemData, keyData []byte) (*x509.Certificate, crypto.PrivateKey, error) {
	pemBlock, _ := pem.Decode(pemData)
	if pemBlock == nil {
		return nil, nil, fmt.Errorf("decode cert PEM: failed")
	}
	cert, err := x509.ParseCertificate(pemBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse cert: %w", err)
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

// takes ~3 ms
func GenerateCert(rootCert *x509.Certificate, rootKey crypto.PrivateKey, host string) (*tls.Certificate, error) {
	sk, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	pk := sk.Public()

	serial, err := generateSerial()
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization:       []string{appid.UserAppName + " Development"},
			OrganizationalUnit: []string{"Containers & Services"},
			// CommonName not needed - we only use SAN
		},
		DNSNames: []string{host},

		NotBefore: time.Now(),
		// from mkcert: 2yr, 3mo due to macOS 825-day limit: https://support.apple.com/en-us/HT210176
		NotAfter: time.Now().AddDate(2, 3, 0),

		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	cert, err := x509.CreateCertificate(rand.Reader, template, rootCert, pk, rootKey)
	if err != nil {
		return nil, fmt.Errorf("create cert: %w", err)
	}

	// skip tls.X509KeyPair and pass data directly for efficiency and simplicity
	return &tls.Certificate{
		// DER block
		// passing full chain with root CA doesn't really help with anything (because it wouldn't be trusted anyway if not installed in system), but still do it
		// otherwise it's technically an invalid chain
		// changes curl error from "unable to get local issuer certificate" to "self signed certificate in certificate chain"
		Certificate: [][]byte{cert, rootCert.Raw},
		PrivateKey:  sk,
	}, nil
}

// from mkcert
func generateSerial() (*big.Int, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, serialNumberLimit)
}

func generateKey() (*ecdsa.PrivateKey, error) {
	// ECDSA is fast to generate
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// from mkcert
func GenerateRoot() (string, string, error) {
	sk, err := generateKey()
	if err != nil {
		return "", "", err
	}
	pk := sk.Public()

	// marshal PK into SPKI ASN.1 format, then unmershal to get raw PK bytes
	spkiASN1, err := x509.MarshalPKIXPublicKey(pk)
	if err != nil {
		return "", "", err
	}

	var spki struct {
		Algorithm        pkix.AlgorithmIdentifier
		SubjectPublicKey asn1.BitString
	}
	_, err = asn1.Unmarshal(spkiASN1, &spki)
	if err != nil {
		return "", "", err
	}

	serial, err := generateSerial()
	if err != nil {
		return "", "", err
	}

	// Subject Key Identifier = sha1 of PK bytes
	skid := sha1.Sum(spki.SubjectPublicKey.Bytes)

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization:       []string{appid.UserAppName + " Development Root CA"},
			OrganizationalUnit: []string{"Containers & Services"},

			CommonName: appid.UserAppName + " Development Root CA",
		},
		SubjectKeyId: skid[:],

		// 10 years
		NotAfter:  time.Now().AddDate(10, 0, 0),
		NotBefore: time.Now(),

		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,

		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	cert, err := x509.CreateCertificate(rand.Reader, template, template, pk, sk)
	if err != nil {
		return "", "", fmt.Errorf("create cert: %w", err)
	}

	skDER, err := x509.MarshalPKCS8PrivateKey(sk)
	if err != nil {
		return "", "", fmt.Errorf("marshal key: %w", err)
	}

	skPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: skDER,
	})

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	})

	return string(certPEM), string(skPEM), nil
}
