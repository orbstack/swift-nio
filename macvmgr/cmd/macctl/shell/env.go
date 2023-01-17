package shell

import (
	"encoding/json"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/sshtypes"
)

var (
	parsedMeta *sshtypes.MacMeta
)

func Meta() sshtypes.MacMeta {
	if parsedMeta == nil {
		envData := os.Getenv("__MV_META")
		var meta sshtypes.MacMeta
		err := json.Unmarshal([]byte(envData), &meta)
		if err != nil {
			if envData != "" {
				panic(err)
			}
		}

		parsedMeta = &meta
	}

	return *parsedMeta
}
