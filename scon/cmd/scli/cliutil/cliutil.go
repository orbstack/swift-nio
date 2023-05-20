package cliutil

import (
	"encoding/json"
	"os"
)

func PrintJSON(obj any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	err := enc.Encode(obj)
	if err != nil {
		panic(err)
	}
}
