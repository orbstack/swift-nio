package zstdframe

import (
	"encoding/binary"
	"fmt"
	"io"
)

// zstd skippable frame: magic 0x184D2A5C, then 4-byte little-endian size, then data
const skippableMagic = "\x5c\x2a\x4d\x18"

// orbstack magic (little-endian): 07 b5 1a cc ("orbstack")
// our zstd frame data consists of: orbstack magic (little-endian), orbstack version (little-endian, 4 bytes), data
const orbMagic = "\xcc\x1a\xb5\x07"

// max size to prevent DoS
const maxSkippableFrameSize = 32 * 1024 * 1024 // 32 MiB

const (
	// only *breaking* changes get a version bump
	VersionMachineConfig1 = (0 << 16) | 1

	VersionDockerVolumeConfig1 = (1 << 16) | 1
)

func WriteSkippable(w io.Writer, orbVersion uint32, data []byte) error {
	var header [4 + 4 + 4 + 4]byte
	copy(header[:4], skippableMagic)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(data)+8))
	copy(header[8:12], orbMagic)
	binary.LittleEndian.PutUint32(header[12:16], orbVersion)

	_, err := w.Write(header[:])
	if err != nil {
		return err
	}

	_, err = w.Write(data)
	return err
}

func ReadSkippable(r io.Reader) (uint32, []byte, error) {
	var header [4 + 4 + 4 + 4]byte
	_, err := r.Read(header[:])
	if err != nil {
		return 0, nil, err
	}

	if string(header[:4]) != skippableMagic {
		return 0, nil, fmt.Errorf("invalid skippable magic: %v", header[:4])
	}

	size := binary.LittleEndian.Uint32(header[4:8])
	if size > maxSkippableFrameSize {
		return 0, nil, fmt.Errorf("skippable frame payload too large: %d", size)
	}

	data := make([]byte, size-8)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return 0, nil, err
	}

	orbVersion := binary.LittleEndian.Uint32(header[12:16])
	return orbVersion, data, nil
}
