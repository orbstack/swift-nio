package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
)

// https://www.kernel.org/doc/html/latest/bpf/btf.html#btf-ext-section
type btfHeader struct {
	Magic   uint16
	Version uint8
	Flags   uint8
	HdrLen  uint32

	TypeOff uint32
	TypeLen uint32
	StrOff  uint32
	StrLen  uint32
}

type btfExtHeader struct {
	Magic   uint16
	Version uint8
	Flags   uint8
	HdrLen  uint32

	FuncInfoOff uint32
	FuncInfoLen uint32
	LineInfoOff uint32
	LineInfoLen uint32
}

type btfExtInfoSec struct {
	SecNameOff uint32
	NumInfo    uint32
}

type btfLineInfo struct {
	InsnOff     uint32
	FileNameOff uint32
	LineOff     uint32
	LineCol     uint32
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func stripOneFile(path string) {
	origElfF, err := os.Open(path)
	check(err)
	defer origElfF.Close()

	// load
	elfFile, err := elf.NewFile(origElfF)
	check(err)

	btfData, err := elfFile.Section(".BTF").Data()
	check(err)

	// parse btf header to find strings table
	btfHdr := btfHeader{}
	btfReader := bytes.NewReader(btfData)
	err = binary.Read(btfReader, binary.LittleEndian, &btfHdr)
	check(err)

	// all offsets are after the header!
	stringsTable := btfData[btfHdr.HdrLen+btfHdr.StrOff : btfHdr.HdrLen+btfHdr.StrOff+btfHdr.StrLen]

	// parse btf ext header to find line infos
	btfExtData, err := elfFile.Section(".BTF.ext").Data()
	check(err)

	btfExtHdr := btfExtHeader{}
	btfExtReader := bytes.NewReader(btfExtData)
	err = binary.Read(btfExtReader, binary.LittleEndian, &btfExtHdr)
	check(err)

	// get line info record size
	lineInfoRecordSize := uint32(0)
	//fmt.Println("btf ext hdr", btfExtHdr)
	// all offsets are after the header!
	// skip the difference first
	btfExtReader.Seek(int64(btfExtHdr.HdrLen+btfExtHdr.LineInfoOff), 0)
	if btfExtHdr.LineInfoLen > 0 {
		err = binary.Read(btfExtReader, binary.LittleEndian, &lineInfoRecordSize)
		check(err)
	}
	//fmt.Println("line info record size", lineInfoRecordSize)

	// read each btf_ext_info_sec
	lineInfoPos := 4
	for {
		if lineInfoPos >= int(btfExtHdr.LineInfoLen) {
			break
		}

		sec := btfExtInfoSec{}
		err = binary.Read(btfExtReader, binary.LittleEndian, &sec)
		check(err)

		// now read numInfo * lineInfoRecordSize items
		lineInfoPos += 8
		for i := uint32(0); i < sec.NumInfo; i++ {
			lineInfo := btfLineInfo{}
			err = binary.Read(btfExtReader, binary.LittleEndian, &lineInfo)
			check(err)

			//fmt.Println("line info", lineInfo)
			// replace with x's in strings table, until null byte
			for j := lineInfo.FileNameOff; j < uint32(len(stringsTable)); j++ {
				if stringsTable[j] == 0 {
					break
				}
				stringsTable[j] = '\x00'
			}
			for j := lineInfo.LineOff; j < uint32(len(stringsTable)); j++ {
				if stringsTable[j] == 0 {
					break
				}
				stringsTable[j] = '\x00'
			}

			lineInfoPos += int(lineInfoRecordSize)
		}
	}

	// zero out the line info offset and len
	btfExtHdr.LineInfoOff = 0
	btfExtHdr.LineInfoLen = 0
	// write to btfExtData
	btfExtWriter := bytes.NewBuffer(btfExtData)
	err = binary.Write(btfExtWriter, binary.LittleEndian, &btfExtHdr)
	check(err)

	// save
	secF, err := os.CreateTemp("", "btfsec")
	check(err)
	defer secF.Close()
	defer os.Remove(secF.Name())

	_, err = secF.Write(btfData)
	check(err)

	// save .BTF.ext
	secFE, err := os.CreateTemp("", "btfEsec")
	check(err)
	defer secFE.Close()
	defer os.Remove(secFE.Name())

	_, err = secFE.Write(btfExtWriter.Bytes())
	check(err)

	// replace .BTF, keep .BTF.ext with func_info for timer callbacks
	cmd := exec.Command("llvm-objcopy", "--update-section=.BTF.ext="+secFE.Name(), "--update-section=.BTF="+secF.Name(), path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	check(err)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: btfstrip <file...>")
		os.Exit(1)
	}

	for _, path := range os.Args[1:] {
		stripOneFile(path)
	}
}
