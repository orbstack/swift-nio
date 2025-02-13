package portprober

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/util/portprober/sillytls"
	"golang.org/x/crypto/cryptobyte"
)

func writeTLSClientHello(w io.Writer, serverName string) (bool, error) {
	hello := &sillytls.Handshake{
		Message: &sillytls.HandshakeClientHello{
			Version:   0x0303,
			Random:    []byte(" OrbStack server detection. More"),
			SessionID: []byte("at: https://orbsta.cc/srvdetect "),
			CipherSuites: []uint16{
				tls.TLS_AES_128_GCM_SHA256,
				tls.TLS_AES_256_GCM_SHA384,
				tls.TLS_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
				tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
				tls.TLS_RSA_WITH_RC4_128_SHA,
				tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
			},
			CompressionMethods:           []uint8{0x00},
			SupportedVersions:            []uint16{0x0304, 0x0303, 0x0302, 0x0301},
			ServerName:                   strings.TrimSuffix(serverName, "."),
			ALPNProtocols:                []string{"h2", "http/1.1"},
			SupportedGroups:              []sillytls.GroupID{tls.CurveP256, tls.CurveP384, tls.CurveP521, tls.X25519},
			SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.PKCS1WithSHA256, tls.PKCS1WithSHA384, tls.PKCS1WithSHA512, tls.PSSWithSHA256, tls.PSSWithSHA384, tls.PSSWithSHA512, tls.ECDSAWithP256AndSHA256, tls.ECDSAWithP384AndSHA384, tls.ECDSAWithP521AndSHA512, tls.Ed25519, tls.PKCS1WithSHA1, tls.ECDSAWithSHA1},
			KeyShares:                    []sillytls.KeyShare{},
		},
	}

	var b cryptobyte.Builder
	hello.Marshal(&b)
	helloBytes, err := b.Bytes()
	if err != nil {
		return false, err
	}

	record := &sillytls.Record{
		LegacyVersion: 0x0301,
		ContentType:   hello.TLSRecordType(),
		Content:       helloBytes,
	}

	err = record.Write(w)
	if err != nil {
		return false, nil
	}

	return true, nil
}

func probePortHTTPS(ctx context.Context, dialer *net.Dialer, host string, port uint16, serverName string) (bool, error) {
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return false, nil
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	success, err := writeTLSClientHello(conn, serverName)
	if err != nil {
		return false, err
	}
	if !success {
		return false, nil
	}

	response := &sillytls.Record{}
	err = response.Read(conn)
	if err != nil {
		return false, nil
	}

	content := sillytls.GetRecordContentForType(response.ContentType)
	if content == nil {
		return false, nil
	}

	contentString := cryptobyte.String(response.Content)
	err = content.Unmarshal(&contentString)
	if err != nil {
		return false, nil
	}

	return true, nil
}
