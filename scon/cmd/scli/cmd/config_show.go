package cmd

import (
	"encoding/json"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
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
		config, err := vmclient.Client().GetConfig()
		check(err)

		// serialize to json
		jsonData, err := json.MarshalIndent(config, "", "  ")
		check(err)

		// deserialize to map
		var configMap map[string]any
		err = json.Unmarshal(jsonData, &configMap)
		check(err)

		// print keys
		for key, value := range configMap {
			cmd.Println(key + ": " + value.(string))
		}

		return nil
	},
}
