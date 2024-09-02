package vclient

import (
	"encoding/base64"
	"encoding/binary"
)

type SeedData struct {
	DataSizeMib      uint64
	InitialDiskStats HostDiskStats

	HostMajorVersion uint16
	HostBuildVersion string

	// reopen console
	ConsolePath string
	// pipe or pty?
	ConsoleIsPipe bool
}

func (d *SeedData) appendString(buf []byte, s string) []byte {
	buf = binary.LittleEndian.AppendUint64(buf, uint64(len(s)))
	buf = append(buf, []byte(s)...)
	return buf
}

func (d *SeedData) appendBool(buf []byte, b bool) []byte {
	if b {
		return append(buf, 1)
	}
	return append(buf, 0)
}

// follows Rust bincode v2 format: https://github.com/bincode-org/bincode/blob/trunk/docs/spec.md
func (d *SeedData) EncodeToBytes() []byte {
	var buf []byte
	buf = binary.LittleEndian.AppendUint64(buf, d.DataSizeMib)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(d.InitialDiskStats.HostFsFree))
	buf = binary.LittleEndian.AppendUint64(buf, uint64(d.InitialDiskStats.DataImgSize))
	buf = binary.LittleEndian.AppendUint16(buf, d.HostMajorVersion)
	buf = d.appendString(buf, d.HostBuildVersion)
	buf = d.appendString(buf, d.ConsolePath)
	buf = d.appendBool(buf, d.ConsoleIsPipe)
	return buf
}

func (d *SeedData) EncodeToString() string {
	b := d.EncodeToBytes()
	return base64.RawURLEncoding.EncodeToString(b)
}
