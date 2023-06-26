package cmd

import (
	"encoding/json"
	"fmt"

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
	Example: "  " + appid.ShortCtl + " show",
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

		// print keys
		for key, value := range configMap {
			fmt.Printf("%s: %v\n", key, value)
		}

		return nil
	},
}
