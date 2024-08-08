package nft

import (
	"fmt"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/util"
)

func Run(args ...string) error {
	combinedArgs := append([]string{"nft"}, args...)
	return util.Run(combinedArgs...)
}

func ApplyConfig(baseConfig string, vars map[string]string) error {
	return Run(FormatConfig(baseConfig, vars))
}

func FormatConfig(baseConfig string, vars map[string]string) string {
	var defines string
	for k, v := range vars {
		defines += fmt.Sprintf("define %s = %s\n", k, v)
	}

	// remove #DEFAULT defines
	lines := strings.Split(baseConfig, "\n")
	for i, line := range lines {
		if strings.Contains(line, "#DEFAULT") {
			lines[i] = ""
		}
	}
	baseConfig = strings.Join(lines, "\n")

	// remove counters in release
	// in debug it doesn't matter: counters are rarely hit due to flowtable
	if !conf.Debug() {
		baseConfig = strings.ReplaceAll(baseConfig, " counter", " ")
	}

	return defines + baseConfig
}
