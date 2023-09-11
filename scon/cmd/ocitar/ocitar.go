package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	TypeFile = 1
	TypeData = 2
)

type TarSplitRecord struct {
	Type int `json:"type"`

	// TypeFile
	//{"type":1,"name":"usr/share/logstash/vendor/bundle/jruby/2.5.0/gems/rufus-scheduler-3.0.9/spec/lock_custom_spec.rb","size":630,"payload":"uGCeMrBUE1c=","position":25343}
	Name string `json:"name"`
	Size int64  `json:"size"`

	// TypeData
	//{"type":2,"payload":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB1c3Ivc2hhcmUvbG9nc3Rhc2gvdmVuZG9yL2J1bmRsZS9qcnVieS8yLjUuMC9nZW1zL3J1ZnVzLXNjaGVkdWxlci0zLjAuOS9saWIvcnVmdXMvc2NoZWR1bGVyL3V0aWwucmIAMDEwMDY2NAAwMDAxNzUwADAwMDAwMDAAMDAwMDAwMjExNTcAMTQxNTQ3NzAzMDMAMDMxMzEyACAwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAHVzdGFyADAwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADAwMDAwMDAAMDAwMDAwMAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==","position":25310}
	Payload []byte `json:"payload"` // base64
}

func findTarSplit(layerDiffId string) (string, error) {
	entries, err := os.ReadDir("/var/lib/docker/image/overlay2/layerdb/sha256")
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		// check if <dir>/diff == layerDiffId
		// if so, return <dir>/tar-split.json.gz
		if !entry.IsDir() {
			continue
		}

		diffId, err := os.ReadFile("/var/lib/docker/image/overlay2/layerdb/sha256/" + entry.Name() + "/diff")
		if err != nil {
			return "", err
		}

		if string(diffId) == layerDiffId {
			return "/var/lib/docker/image/overlay2/layerdb/sha256/" + entry.Name() + "/tar-split.json.gz", nil
		}
	}

	return "", errors.New("not found")
}

func main() {
	runtime.GOMAXPROCS(1)

	destSpec := os.Args[1]
	layerDiffId := os.Args[2]
	srcDir := os.Args[3]

	var writer io.WriteCloser
	var err error
	if destSpec == "-" {
		writer = os.Stdout
	} else {
		writer, err = os.Create(destSpec)
	}
	if err != nil {
		panic(err)
	}
	defer writer.Close()

	// find the tar-split.json.gz
	tarSplitPath, err := findTarSplit(layerDiffId)
	if err != nil {
		panic(err)
	}

	// read it
	tarSplitDataGz, err := os.ReadFile(tarSplitPath)
	if err != nil {
		panic(err)
	}

	// gunzip
	gzReader, err := gzip.NewReader(bytes.NewReader(tarSplitDataGz))
	if err != nil {
		panic(err)
	}
	tarSplitData, err := io.ReadAll(gzReader)
	if err != nil {
		panic(err)
	}

	// open dir
	dirfd, err := unix.Open(srcDir, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		panic(err)
	}
	defer unix.Close(dirfd)

	// null terminated
	pathBuf := make([]byte, unix.PathMax)

	// parse json records
	decoder := json.NewDecoder(bytes.NewReader(tarSplitData))
	var record TarSplitRecord
	for {
		err := decoder.Decode(&record)
		if err != nil {
			if err == io.EOF {
				break
			}
			panic(err)
		}

		switch record.Type {
		case TypeFile:
			// dir / symlink
			if record.Size == 0 {
				continue
			}

			// read file
			// dirfd + pathBuf makes this code 100% allocation-free
			if len(record.Name) > unix.PathMax-1 {
				panic("name too long")
			}
			copy(pathBuf, record.Name)
			pathBuf[len(record.Name)] = 0
			err = Sendfile(dirfd, pathBuf, writer, record.Size)
			if err != nil {
				panic(err)
			}

		case TypeData:
			// write data
			_, err = writer.Write(record.Payload)
			if err != nil {
				panic(err)
			}
		}
	}
}

func Sendfile(dirfd int, pathBuf []byte, writer io.Writer, size int64) error {
	// null terminated
	// RawSyscall avoids calling runtime funcs
	fd, _, err := unix.RawSyscall(unix.SYS_OPENAT, uintptr(dirfd), uintptr(unsafe.Pointer(&pathBuf[0])), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_LARGEFILE)
	if err != 0 {
		return fmt.Errorf("openat: %w", err)
	}
	defer unix.RawSyscall(unix.SYS_CLOSE, uintptr(fd), 0, 0)

	writeFd := int(writer.(*os.File).Fd())
	rem := size
	for rem > 0 {
		written, _, err := unix.RawSyscall6(unix.SYS_SENDFILE, uintptr(writeFd), uintptr(fd), 0, uintptr(rem), 0, 0)
		if err != 0 {
			return fmt.Errorf("sendfile: %w", err)
		}
		rem -= int64(written)
	}

	return nil
}
