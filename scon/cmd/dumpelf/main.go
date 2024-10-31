package main

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"

	"github.com/cilium/ebpf"
)

func main() {
	fileBytes, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	f, err := elf.NewFile(bytes.NewReader(fileBytes))
	if err != nil {
		panic(err)
	}
	for i, section := range f.Sections {
		fmt.Printf("index: %d; section: %+v\n", i, section)
	}

	symbols, err := f.Symbols()
	if err != nil {
		panic(err)
	}
	for _, symbol := range symbols {
		fmt.Printf("symbol: %+v; symbol.Section: %d\n", symbol, symbol.Section)
	}

	cs, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(fileBytes))
	if err != nil {
		panic(err)
	}

	fmt.Printf("maps: %+v", cs.Maps)
}
