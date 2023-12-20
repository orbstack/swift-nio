package cmd

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configShowCmd)
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	Long: `Show the current configuration for the Linux virtual machine.
`,
	Example: "  " + appid.ShortCmd + " show",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureVMWithSpinner()
		config, err := vmclient.Client().GetConfig()
		checkCLI(err)

		// serialize to json
		jsonData, err := json.MarshalIndent(config, "", "  ")
		checkCLI(err)

		// deserialize to map
		var configMap map[string]any
		err = json.Unmarshal(jsonData, &configMap)
		checkCLI(err)

		// print keys in sorted order
		lines := make([]string, 0, len(configMap))
		for key, value := range configMap {
			lines = append(lines, fmt.Sprintf("%s: %v", key, value))
		}
		slices.Sort(lines)
		for _, line := range lines {
			fmt.Println(line)
		}

		return nil
	},
}
