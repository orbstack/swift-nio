package cmd

import (
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

		// show as yaml
		yamlData, err := yaml.Marshal(config)
		check(err)

		cmd.Println(string(yamlData))
		return nil
	},
}
