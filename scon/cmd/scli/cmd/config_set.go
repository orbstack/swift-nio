package cmd

import (
	"os"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
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

Some options will only take effect after restarting the virtual machine.
`,
	Example: "  " + appid.ShortCtl + " set memory_mib 4096",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var config vmconfig.VmConfig

		key := args[0]
		value := args[1]
		var err error
		rebootRequired := false
		switch key {
		case "memory_mib":
			config.MemoryMiB, err = strconv.ParseUint(value, 10, 64)
			rebootRequired = true
		default:
			cmd.PrintErrln("Unknown configuration key:", key)
			os.Exit(1)
		}
		checkCLI(err)

		scli.EnsureVMWithSpinner()
		err = vmclient.Client().PatchConfig(&config)
		checkCLI(err)

		if rebootRequired {
			cmd.Println(`Restart the VM with "` + appid.ShortCtl + ` shutdown" to apply changes.`)
		}

		return nil
	},
}
