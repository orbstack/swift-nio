package sillytls

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"golang.org/x/crypto/cryptobyte"
)

func jsonPrettyPrint(v any) string {
	s, _ := json.MarshalIndent(v, "", "  ")
	return string(s)
}

func TestClientHello(t *testing.T) {
	hello := &Handshake{
		Message: &HandshakeClientHello{
			Version:                      0x0301,
			Random:                       []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
			SessionID:                    []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f},
			CipherSuites:                 []uint16{0x002f, 0x0033},
			CompressionMethods:           []uint8{0x00, 0x01},
			SupportedVersions:            []uint16{0x0303, 0x0304, 0x302, 0x301},
			ServerName:                   "www.example.com",
			ALPNProtocols:                []string{"h2", "http/1.1"},
			SupportedGroups:              []tls.CurveID{tls.X25519, tls.CurveP256},
			SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.Ed25519, tls.ECDSAWithP256AndSHA256},
			KeyShares: []KeyShare{
				{
					Group: uint16(tls.X25519),
					Data:  []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
				},
			},
		},
	}
	t.Logf("hello: %s\n", jsonPrettyPrint(hello))

	var contentBuilder cryptobyte.Builder
	if err := hello.Marshal(&contentBuilder); err != nil {
		t.Fatal(err)
	}
	contentBytes, err := contentBuilder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	record := &Record{
		LegacyVersion: 0x0303,
		ContentType:   hello.TLSRecordType(),
		Content:       contentBytes,
	}
	t.Logf("record: %s\n", jsonPrettyPrint(record))

	var recordBuilder cryptobyte.Builder
	if err := record.Marshal(&recordBuilder); err != nil {
		t.Fatal(err)
	}
	recordBytes, err := recordBuilder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("recordBytes: %x\n", recordBytes)

	recordUnmarshaled := &Record{}
	recordString := cryptobyte.String(recordBytes)
	if err := recordUnmarshaled.Unmarshal(&recordString); err != nil {
		t.Fatal(err)
	}
	t.Logf("recordUnmarshaled: %s\n", jsonPrettyPrint(recordUnmarshaled))
	if !reflect.DeepEqual(record, recordUnmarshaled) {
		t.Fatal("record != recordUnmarshaled")
	}

	helloUnmarshaled := &Handshake{}
	contentString := cryptobyte.String(recordUnmarshaled.Content)
	if err := helloUnmarshaled.Unmarshal(&contentString); err != nil {
		t.Fatal(err)
	}
	t.Logf("helloUnmarshaled: %s\n", jsonPrettyPrint(helloUnmarshaled))
	if !reflect.DeepEqual(hello, helloUnmarshaled) {
		t.Logf("\n==== expected ====\n%s\n==== got ====\n%s\n", jsonPrettyPrint(hello), jsonPrettyPrint(helloUnmarshaled))
		t.Fatal("hello != helloUnmarshaled")
	}
}

func TestRecordWriteRead(t *testing.T) {
	record := &Record{
		LegacyVersion: 0x0301,
		ContentType:   22,
		Content:       []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
	}

	var data bytes.Buffer
	if err := record.Write(&data); err != nil {
		t.Fatal(err)
	}
	t.Logf("data: %x\n", data.Bytes())

	recordUnmarshaled := &Record{}
	if err := recordUnmarshaled.Read(&data); err != nil {
		t.Fatal(err)
	}
	t.Logf("recordUnmarshaled: %s\n", jsonPrettyPrint(recordUnmarshaled))
	if !reflect.DeepEqual(record, recordUnmarshaled) {
		t.Fatal("record != recordUnmarshaled")
	}
}

func TestAlert(t *testing.T) {
	for alert, text := range alertText {
		alert := &Alert{
			Level:   AlertLevelWarning,
			Message: alert,
		}
		var alertBuilder cryptobyte.Builder
		if err := alert.Marshal(&alertBuilder); err != nil {
			t.Fatal(err)
		}
		alertBytes, err := alertBuilder.Bytes()
		if err != nil {
			t.Fatal(err)
		}

		alertUnmarshaled := &Alert{}
		alertString := cryptobyte.String(alertBytes)
		if err := alertUnmarshaled.Unmarshal(&alertString); err != nil {
			t.Fatal(err)
		}
		t.Logf("alertUnmarshaled: %s\n", jsonPrettyPrint(alertUnmarshaled))

		if !reflect.DeepEqual(alert, alertUnmarshaled) {
			t.Fatalf("alert != alertUnmarshaled")
		}

		if alertUnmarshaled.String() != fmt.Sprintf("%s: %s", alert.Level.String(), text) {
			t.Fatalf("alertUnmarshaled.String() != %s: %s", alert.Level.String(), text)
		}
	}
}
