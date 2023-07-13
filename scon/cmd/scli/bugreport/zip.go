package bugreport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type ReportWriter struct {
	buffer bytes.Buffer
	zip    *zip.Writer
}

func newReport() *ReportWriter {
	r := &ReportWriter{}
	r.zip = zip.NewWriter(&r.buffer)
	return r
}

func (r *ReportWriter) addFileBytes(name string, b []byte) error {
	f, err := r.zip.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return err
}

func (r *ReportWriter) AddFileLocal(src, dest string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		} else {
			return err
		}
	}
	return r.addFileBytes(dest, b)
}

func (r *ReportWriter) AddDirLocal(src, dest string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		} else {
			return err
		}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			err = r.AddDirLocal(src+"/"+entry.Name(), dest+"/"+entry.Name())
		} else {
			err = r.AddFileLocal(src+"/"+entry.Name(), dest+"/"+entry.Name())
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ReportWriter) AddFileJson(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "    ")
	if err != nil {
		return err
	}

	return r.addFileBytes(name, data)
}

func (r *ReportWriter) Finish() (*ReportPackage, error) {
	err := r.zip.Close()
	if err != nil {
		return nil, err
	}

	data := r.buffer.Bytes()

	// name: ISO8601 date
	name := time.Now().UTC().Format("2006-01-02T15-04-05.000000Z")

	return &ReportPackage{
		Name: "orbstack-diagreport_" + name + ".zip",
		Data: data,
	}, nil
}
