package sillytls

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/cryptobyte"
)

type Marshalable interface {
	Marshal(b *cryptobyte.Builder) error
	Unmarshal(s *cryptobyte.String) error
}

// ==== record layer ====

type Record struct {
	ContentType   uint8
	LegacyVersion uint16
	Content       []byte
}

func (r *Record) Marshal(b *cryptobyte.Builder) error {
	b.AddUint8(r.ContentType)
	b.AddUint16(r.LegacyVersion)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddBytes(r.Content)
	})

	return nil
}

func (r *Record) Unmarshal(s *cryptobyte.String) error {
	if !s.ReadUint8(&r.ContentType) {
		return fmt.Errorf("failed to read content type")
	}
	if !s.ReadUint16(&r.LegacyVersion) {
		return fmt.Errorf("failed to read legacy version")
	}

	var content cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&content) {
		return fmt.Errorf("failed to read content")
	}
	r.Content = content

	return nil
}

func (r *Record) Write(w io.Writer) error {
	err := binary.Write(w, binary.BigEndian, r.ContentType)
	if err != nil {
		return err
	}

	err = binary.Write(w, binary.BigEndian, r.LegacyVersion)
	if err != nil {
		return err
	}

	err = binary.Write(w, binary.BigEndian, uint16(len(r.Content)))
	if err != nil {
		return err
	}

	_, err = w.Write(r.Content)
	return err
}

func (r *Record) Read(re io.Reader) error {
	err := binary.Read(re, binary.BigEndian, &r.ContentType)
	if err != nil {
		return err
	}

	err = binary.Read(re, binary.BigEndian, &r.LegacyVersion)
	if err != nil {
		return err
	}

	var contentLength uint16
	err = binary.Read(re, binary.BigEndian, &contentLength)
	if err != nil {
		return err
	}

	r.Content = make([]byte, contentLength)
	_, err = re.Read(r.Content)
	return err
}

type RecordContent interface {
	Marshalable
	TLSRecordType() uint8
}

func GetRecordContentType(contentType uint8) RecordContent {
	var content RecordContent

	switch contentType {
	case 20:
		content = &ChangeCipherSpec{}
	case 21:
		content = &Alert{}
	case 22:
		content = &Handshake{}
	case 23:
		content = &ApplicationData{}
	}

	if content != nil && content.TLSRecordType() != contentType {
		panic(fmt.Sprintf("tls record content type mismatch: %d != %d", content.TLSRecordType(), contentType))
	}

	return content
}

// ==== handshake protocol ====

type Handshake struct {
	Message HandshakeMessage
}

func (r *Handshake) TLSRecordType() uint8 {
	return 22
}

func (r *Handshake) Marshal(b *cryptobyte.Builder) error {
	b.AddUint8(r.Message.TLSHandshakeType())

	var err error
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		err = r.Message.Marshal(b)
	})
	return err
}

func getHandshakeMessageType(messageType uint8) HandshakeMessage {
	var message HandshakeMessage

	switch messageType {
	case 1:
		message = &HandshakeClientHello{}
	case 2:
		message = &HandshakeServerHello{}
	case 4:
		message = &HandshakeNewSessionTicket{}
	case 5:
		message = &HandshakeEndOfEarlyData{}
	case 8:
		message = &HandshakeEncryptedExtensions{}
	case 11:
		message = &HandshakeCertificate{}
	case 13:
		message = &HandshakeCertificateRequest{}
	case 15:
		message = &HandshakeCertificateVerify{}
	case 20:
		message = &HandshakeFinished{}
	case 24:
		message = &HandshakeKeyUpdate{}
	case 254:
		message = &HandshakeMessageHash{}
	}

	if message != nil && message.TLSHandshakeType() != messageType {
		panic(fmt.Sprintf("tls handshake message type mismatch: %d != %d", message.TLSHandshakeType(), messageType))
	}

	return message
}

func (r *Handshake) Unmarshal(s *cryptobyte.String) error {
	var messageType uint8
	if !s.ReadUint8(&messageType) {
		return fmt.Errorf("failed to read message type")
	}
	r.Message = getHandshakeMessageType(messageType)
	if r.Message == nil {
		return fmt.Errorf("unknown handshake message type: %d", messageType)
	}

	var message cryptobyte.String
	if !s.ReadUint24LengthPrefixed(&message) {
		return fmt.Errorf("failed to read message")
	}

	return r.Message.Unmarshal(&message)
}

type HandshakeMessage interface {
	Marshalable
	TLSHandshakeType() uint8
}

type KeyShare struct {
	Group uint16
	Data  []byte
}

type GroupID = tls.CurveID

type HandshakeClientHello struct {
	Version                          uint16
	Random                           []byte
	SessionID                        []byte
	CipherSuites                     []uint16
	CompressionMethods               []byte
	SupportedVersions                []uint16
	ServerName                       string
	ALPNProtocols                    []string
	SupportedGroups                  []GroupID
	SupportedSignatureAlgorithms     []tls.SignatureScheme
	SupportedSignatureAlgorithmsCert []tls.SignatureScheme
	KeyShares                        []KeyShare
}

func (m *HandshakeClientHello) TLSHandshakeType() uint8 {
	return 1
}

func (m *HandshakeClientHello) Marshal(b *cryptobyte.Builder) error {
	var exts cryptobyte.Builder

	if len(m.ServerName) > 0 {
		exts.AddUint16(0) // extension type = server_name
		exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
			// extension data
			exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
				exts.AddUint8(0) // server_name type = host_name
				exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
					exts.AddBytes([]byte(m.ServerName))
				})
			})
		})
	}

	if m.SupportedVersions != nil {
		exts.AddUint16(43) // extension type = supported_versions
		exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
			// extension data
			exts.AddUint8LengthPrefixed(func(exts *cryptobyte.Builder) {
				for _, version := range m.SupportedVersions {
					exts.AddUint16(version)
				}
			})
		})
	}

	if m.SupportedGroups != nil {
		exts.AddUint16(10) // extension type = supported_groups
		exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
			// extension data
			exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
				for _, group := range m.SupportedGroups {
					exts.AddUint16(uint16(group))
				}
			})
		})
	}

	if m.SupportedSignatureAlgorithms != nil {
		exts.AddUint16(13) // extension type = supported_signature_algorithms
		exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
			// extension data
			exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
				for _, algo := range m.SupportedSignatureAlgorithms {
					exts.AddUint16(uint16(algo))
				}
			})
		})
	}

	if m.SupportedSignatureAlgorithmsCert != nil {
		exts.AddUint16(50) // extension type = supported_signature_algorithms_cert
		exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
			// extension data
			exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
				for _, algo := range m.SupportedSignatureAlgorithmsCert {
					exts.AddUint16(uint16(algo))
				}
			})
		})
	}

	if m.KeyShares != nil {
		exts.AddUint16(51) // extension type = key_share
		exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
			// extension data
			exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
				for _, keyShare := range m.KeyShares {
					exts.AddUint16(uint16(keyShare.Group))
					exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
						exts.AddBytes(keyShare.Data)
					})
				}
			})
		})
	}

	if m.ALPNProtocols != nil {
		exts.AddUint16(16) // extension type = alpn
		exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
			// extension data
			exts.AddUint16LengthPrefixed(func(exts *cryptobyte.Builder) {
				for _, proto := range m.ALPNProtocols {
					exts.AddUint8LengthPrefixed(func(exts *cryptobyte.Builder) {
						exts.AddBytes([]byte(proto))
					})
				}
			})
		})
	}

	b.AddUint16(m.Version)

	if len(m.Random) != 32 {
		return fmt.Errorf("invalid random length: %d", len(m.Random))
	}
	b.AddBytes(m.Random)

	b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddBytes(m.SessionID)
	})

	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		for _, suite := range m.CipherSuites {
			b.AddUint16(suite)
		}
	})

	b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddBytes(m.CompressionMethods)
	})

	extBytes, err := exts.Bytes()
	if err != nil {
		return err
	}
	if len(extBytes) > 0 {
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(extBytes)
		})
	}

	return nil
}

func (m *HandshakeClientHello) Unmarshal(s *cryptobyte.String) error {
	if !s.ReadUint16(&m.Version) {
		return fmt.Errorf("failed to read version")
	}

	if !s.ReadBytes(&m.Random, 32) {
		return fmt.Errorf("failed to read random")
	}

	if !s.ReadUint8LengthPrefixed((*cryptobyte.String)(&m.SessionID)) {
		return fmt.Errorf("failed to read session id")
	}

	var cipherSuitesBytes cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&cipherSuitesBytes) {
		return fmt.Errorf("failed to read cipher suites")
	}
	for !cipherSuitesBytes.Empty() {
		var cipherSuite uint16
		if !cipherSuitesBytes.ReadUint16(&cipherSuite) {
			return fmt.Errorf("failed to read cipher suite")
		}
		m.CipherSuites = append(m.CipherSuites, cipherSuite)
	}

	if !s.ReadUint8LengthPrefixed((*cryptobyte.String)(&m.CompressionMethods)) {
		return fmt.Errorf("failed to read compression methods")
	}

	var extsBytes cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&extsBytes) {
		return fmt.Errorf("failed to read extensions")
	}
	for !extsBytes.Empty() {
		var extType uint16
		if !extsBytes.ReadUint16(&extType) {
			return fmt.Errorf("failed to read extension type")
		}
		var extData cryptobyte.String
		if !extsBytes.ReadUint16LengthPrefixed(&extData) {
			return fmt.Errorf("failed to read extension data")
		}

		switch extType {
		case 0: // extension type = server_name
			var serverNameBytes cryptobyte.String
			if !extData.ReadUint16LengthPrefixed(&serverNameBytes) {
				return fmt.Errorf("failed to read server name type")
			}

			serverNameBytes.Skip(1) // skip server_name type

			var serverName cryptobyte.String
			if !serverNameBytes.ReadUint16LengthPrefixed(&serverName) {
				return fmt.Errorf("failed to read server name")
			}
			m.ServerName = string(serverName)
		case 43: // extension type = supported_versions
			var supportedVersionsBytes cryptobyte.String
			if !extData.ReadUint8LengthPrefixed(&supportedVersionsBytes) {
				return fmt.Errorf("failed to read supported versions")
			}
			for !supportedVersionsBytes.Empty() {
				var version uint16
				if !supportedVersionsBytes.ReadUint16(&version) {
					return fmt.Errorf("failed to read supported version")
				}
				m.SupportedVersions = append(m.SupportedVersions, version)
			}
		case 16: // extension type = alpn
			var alpnProtocolsBytes cryptobyte.String
			if !extData.ReadUint16LengthPrefixed(&alpnProtocolsBytes) {
				return fmt.Errorf("failed to read alpn protocols")
			}
			for !alpnProtocolsBytes.Empty() {
				var proto cryptobyte.String
				if !alpnProtocolsBytes.ReadUint8LengthPrefixed(&proto) {
					return fmt.Errorf("failed to read alpn protocol")
				}
				m.ALPNProtocols = append(m.ALPNProtocols, string(proto))
			}
		case 10: // extension type = supported_groups
			var supportedGroupsBytes cryptobyte.String
			if !extData.ReadUint16LengthPrefixed(&supportedGroupsBytes) {
				return fmt.Errorf("failed to read supported groups")
			}
			for !supportedGroupsBytes.Empty() {
				var group uint16
				if !supportedGroupsBytes.ReadUint16(&group) {
					return fmt.Errorf("failed to read supported group")
				}
				m.SupportedGroups = append(m.SupportedGroups, tls.CurveID(group))
			}
		case 13: // extension type = supported_signature_algorithms
			var supportedSignatureAlgorithmsBytes cryptobyte.String
			if !extData.ReadUint16LengthPrefixed(&supportedSignatureAlgorithmsBytes) {
				return fmt.Errorf("failed to read supported signature algorithms")
			}
			for !supportedSignatureAlgorithmsBytes.Empty() {
				var algo uint16
				if !supportedSignatureAlgorithmsBytes.ReadUint16(&algo) {
					return fmt.Errorf("failed to read supported signature algorithm")
				}
				m.SupportedSignatureAlgorithms = append(m.SupportedSignatureAlgorithms, tls.SignatureScheme(algo))
			}
		case 50: // extension type = supported_signature_algorithms_cert
			var supportedSignatureAlgorithmsCertBytes cryptobyte.String
			if !extData.ReadUint16LengthPrefixed(&supportedSignatureAlgorithmsCertBytes) {
				return fmt.Errorf("failed to read supported signature algorithms cert")
			}
			for !supportedSignatureAlgorithmsCertBytes.Empty() {
				var algo uint16
				if !supportedSignatureAlgorithmsCertBytes.ReadUint16(&algo) {
					return fmt.Errorf("failed to read supported signature algorithm cert")
				}
				m.SupportedSignatureAlgorithmsCert = append(m.SupportedSignatureAlgorithmsCert, tls.SignatureScheme(algo))
			}
		case 51: // extension type = key_share
			var keySharesBytes cryptobyte.String
			if !extData.ReadUint16LengthPrefixed(&keySharesBytes) {
				return fmt.Errorf("failed to read key shares")
			}
			for !keySharesBytes.Empty() {
				var keyShare KeyShare

				if !keySharesBytes.ReadUint16(&keyShare.Group) {
					return fmt.Errorf("failed to read key share group")
				}

				if !keySharesBytes.ReadUint16LengthPrefixed((*cryptobyte.String)(&keyShare.Data)) {
					return fmt.Errorf("failed to read key share data")
				}

				m.KeyShares = append(m.KeyShares, keyShare)
			}
		}
	}

	return nil
}

type HandshakeServerHello struct {
	Data []byte
}

func (m *HandshakeServerHello) TLSHandshakeType() uint8 {
	return 2
}

func (m *HandshakeServerHello) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeServerHello) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeNewSessionTicket struct {
	Data []byte
}

func (m *HandshakeNewSessionTicket) TLSHandshakeType() uint8 {
	return 4
}

func (m *HandshakeNewSessionTicket) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeNewSessionTicket) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeEndOfEarlyData struct {
	Data []byte
}

func (m *HandshakeEndOfEarlyData) TLSHandshakeType() uint8 {
	return 5
}

func (m *HandshakeEndOfEarlyData) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeEndOfEarlyData) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeEncryptedExtensions struct {
	Data []byte
}

func (m *HandshakeEncryptedExtensions) TLSHandshakeType() uint8 {
	return 8
}

func (m *HandshakeEncryptedExtensions) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeEncryptedExtensions) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeCertificate struct {
	Data []byte
}

func (m *HandshakeCertificate) TLSHandshakeType() uint8 {
	return 11
}

func (m *HandshakeCertificate) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeCertificate) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeCertificateRequest struct {
	Data []byte
}

func (m *HandshakeCertificateRequest) TLSHandshakeType() uint8 {
	return 13
}

func (m *HandshakeCertificateRequest) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeCertificateRequest) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeCertificateVerify struct {
	Data []byte
}

func (m *HandshakeCertificateVerify) TLSHandshakeType() uint8 {
	return 15
}

func (m *HandshakeCertificateVerify) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeCertificateVerify) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeFinished struct {
	Data []byte
}

func (m *HandshakeFinished) TLSHandshakeType() uint8 {
	return 20
}

func (m *HandshakeFinished) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeFinished) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeKeyUpdate struct {
	Data []byte
}

func (m *HandshakeKeyUpdate) TLSHandshakeType() uint8 {
	return 24
}

func (m *HandshakeKeyUpdate) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeKeyUpdate) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

type HandshakeMessageHash struct {
	Data []byte
}

func (m *HandshakeMessageHash) TLSHandshakeType() uint8 {
	return 25
}

func (m *HandshakeMessageHash) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(m.Data)
	return nil
}

func (m *HandshakeMessageHash) Unmarshal(s *cryptobyte.String) error {
	m.Data = *s
	return nil
}

// ==== alert protocol ====

type AlertMessage uint8

const (
	AlertCloseNotify                  AlertMessage = 0
	AlertUnexpectedMessage            AlertMessage = 10
	AlertBadRecordMAC                 AlertMessage = 20
	AlertDecryptionFailed             AlertMessage = 21
	AlertRecordOverflow               AlertMessage = 22
	AlertDecompressionFailure         AlertMessage = 30
	AlertHandshakeFailure             AlertMessage = 40
	AlertBadCertificate               AlertMessage = 42
	AlertUnsupportedCertificate       AlertMessage = 43
	AlertCertificateRevoked           AlertMessage = 44
	AlertCertificateExpired           AlertMessage = 45
	AlertCertificateUnknown           AlertMessage = 46
	AlertIllegalParameter             AlertMessage = 47
	AlertUnknownCA                    AlertMessage = 48
	AlertAccessDenied                 AlertMessage = 49
	AlertDecodeError                  AlertMessage = 50
	AlertDecryptError                 AlertMessage = 51
	AlertExportRestriction            AlertMessage = 60
	AlertProtocolVersion              AlertMessage = 70
	AlertInsufficientSecurity         AlertMessage = 71
	AlertInternalError                AlertMessage = 80
	AlertInappropriateFallback        AlertMessage = 86
	AlertUserCanceled                 AlertMessage = 90
	AlertNoRenegotiation              AlertMessage = 100
	AlertMissingExtension             AlertMessage = 109
	AlertUnsupportedExtension         AlertMessage = 110
	AlertCertificateUnobtainable      AlertMessage = 111
	AlertUnrecognizedName             AlertMessage = 112
	AlertBadCertificateStatusResponse AlertMessage = 113
	AlertBadCertificateHashValue      AlertMessage = 114
	AlertUnknownPSKIdentity           AlertMessage = 115
	AlertCertificateRequired          AlertMessage = 116
	AlertNoApplicationProtocol        AlertMessage = 120
	AlertECHRequired                  AlertMessage = 121
)

var alertText = map[AlertMessage]string{
	AlertCloseNotify:                  "close notify",
	AlertUnexpectedMessage:            "unexpected message",
	AlertBadRecordMAC:                 "bad record MAC",
	AlertDecryptionFailed:             "decryption failed",
	AlertRecordOverflow:               "record overflow",
	AlertDecompressionFailure:         "decompression failure",
	AlertHandshakeFailure:             "handshake failure",
	AlertBadCertificate:               "bad certificate",
	AlertUnsupportedCertificate:       "unsupported certificate",
	AlertCertificateRevoked:           "revoked certificate",
	AlertCertificateExpired:           "expired certificate",
	AlertCertificateUnknown:           "unknown certificate",
	AlertIllegalParameter:             "illegal parameter",
	AlertUnknownCA:                    "unknown certificate authority",
	AlertAccessDenied:                 "access denied",
	AlertDecodeError:                  "error decoding message",
	AlertDecryptError:                 "error decrypting message",
	AlertExportRestriction:            "export restriction",
	AlertProtocolVersion:              "protocol version not supported",
	AlertInsufficientSecurity:         "insufficient security level",
	AlertInternalError:                "internal error",
	AlertInappropriateFallback:        "inappropriate fallback",
	AlertUserCanceled:                 "user canceled",
	AlertNoRenegotiation:              "no renegotiation",
	AlertMissingExtension:             "missing extension",
	AlertUnsupportedExtension:         "unsupported extension",
	AlertCertificateUnobtainable:      "certificate unobtainable",
	AlertUnrecognizedName:             "unrecognized name",
	AlertBadCertificateStatusResponse: "bad certificate status response",
	AlertBadCertificateHashValue:      "bad certificate hash value",
	AlertUnknownPSKIdentity:           "unknown PSK identity",
	AlertCertificateRequired:          "certificate required",
	AlertNoApplicationProtocol:        "no application protocol",
	AlertECHRequired:                  "encrypted client hello required",
}

func (a AlertMessage) String() string {
	return alertText[a]
}

type AlertLevel uint8

const (
	AlertLevelWarning AlertLevel = 1
	AlertLevelFatal   AlertLevel = 2
)

func (a AlertLevel) String() string {
	switch a {
	case AlertLevelWarning:
		return "warning"
	case AlertLevelFatal:
		return "fatal"
	}
	return "unknown"
}

type Alert struct {
	Level   AlertLevel
	Message AlertMessage
}

func (a *Alert) TLSRecordType() uint8 {
	return 21
}

func (a *Alert) Marshal(b *cryptobyte.Builder) error {
	b.AddUint8(uint8(a.Level))
	b.AddUint8(uint8(a.Message))
	return nil
}

func (a *Alert) Unmarshal(s *cryptobyte.String) error {
	var level uint8
	if !s.ReadUint8(&level) {
		return fmt.Errorf("failed to read level")
	}
	a.Level = AlertLevel(level)

	var message uint8
	if !s.ReadUint8(&message) {
		return fmt.Errorf("failed to read message")
	}
	a.Message = AlertMessage(message)

	return nil
}

func (a *Alert) String() string {
	return fmt.Sprintf("%s: %s", a.Level, a.Message)
}

// ==== change cipher spec protocol ====

type ChangeCipherSpec struct {
	Data []byte
}

func (c *ChangeCipherSpec) TLSRecordType() uint8 {
	return 20
}

func (c *ChangeCipherSpec) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(c.Data)
	return nil
}

func (c *ChangeCipherSpec) Unmarshal(s *cryptobyte.String) error {
	c.Data = *s
	return nil
}

// ==== application data protocol ====

type ApplicationData struct {
	Data []byte
}

func (a *ApplicationData) TLSRecordType() uint8 {
	return 23
}

func (a *ApplicationData) Marshal(b *cryptobyte.Builder) error {
	b.AddBytes(a.Data)
	return nil
}

func (a *ApplicationData) Unmarshal(s *cryptobyte.String) error {
	a.Data = *s
	return nil
}
