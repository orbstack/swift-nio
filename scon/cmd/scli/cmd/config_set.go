package cmd

import (
	"os"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configSetCmd)
}

var configSetCmd = &cobra.Command{
	Use:   "set KEY VALUE",
	Short: "Set a configuration option",
	Long: `Set a single configuration option for the Linux virtual machine.

Supported options: memory_mib
`,
	Example: "  " + appid.ShortCtl + " set memory_mib 4096",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var config vmconfig.VmConfig

		key := args[0]
		value := args[1]
		var err error
		switch key {
		case "memory_mib":
			config.MemoryMiB, err = strconv.ParseUint(value, 10, 64)
		default:
			cmd.PrintErrln("Unknown configuration key:", key)
			os.Exit(1)
		}
		check(err)

		err = vmclient.Client().PatchConfig(&config)
		check(err)

		return nil
	},
}
