package main

import (
	"os"
)

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	for i := 0; i < len(data); i++ {
		data[i] ^= 0x5a
	}

	err = os.WriteFile(os.Args[1], data, 0644)
	if err != nil {
		panic(err)
	}
}
