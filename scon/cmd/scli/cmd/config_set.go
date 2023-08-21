package cmd

import (
	"errors"
	"os"
	"runtime"
	"strconv"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configSetCmd)
}

var configSetCmd = &cobra.Command{
	Use:   "set KEY VALUE",
	Short: "Set a config option",
	Long: `Set a single configuration option for the Linux virtual machine.

See "orb config show" for a list of options.

Some options will only take effect after restarting the virtual machine.
`,
	Example: "  " + appid.ShortCmd + " set memory_mib 4096",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureVMWithSpinner()

		config, err := vmclient.Client().GetConfig()
		checkCLI(err)
		oldConfig := *config

		key := args[0]
		value := args[1]
		rebootRequired := false
		switch key {
		case "memory_mib":
			val, err := strconv.ParseUint(value, 10, 64)
			checkCLI(err)
			config.MemoryMiB = val
			rebootRequired = true
		case "cpu":
			val, err := strconv.ParseUint(value, 10, 64)
			checkCLI(err)
			intV := int(val)
			config.CPU = intV

			if intV < 1 {
				checkCLI(errors.New("CPU limit must be at least 1"))
			}
			if intV > runtime.NumCPU() {
				checkCLI(errors.New("CPU limit cannot exceed number of cores"))
			}

			rebootRequired = true
		case "rosetta":
			val, err := strconv.ParseBool(value)
			checkCLI(err)
			config.Rosetta = val
			rebootRequired = true
		case "network_proxy":
			config.NetworkProxy = value
		case "network_bridge":
			val, err := strconv.ParseBool(value)
			checkCLI(err)
			config.NetworkBridge = val
		case "data_dir":
			config.DataDir = value
			rebootRequired = true
		case "docker.set_context":
			val, err := strconv.ParseBool(value)
			checkCLI(err)
			config.DockerSetContext = val
		case "setup.use_admin":
			val, err := strconv.ParseBool(value)
			checkCLI(err)
			config.SetupUseAdmin = val
			rebootRequired = true
		case "k8s.enable":
			val, err := strconv.ParseBool(value)
			checkCLI(err)
			config.K8sEnable = val
			rebootRequired = true
		default:
			cmd.PrintErrln("Unknown configuration key:", key)
			os.Exit(1)
		}

		err = vmclient.Client().SetConfig(config)
		checkCLI(err)

		if rebootRequired {
			cmd.Println(`Restart OrbStack with "` + appid.ShortCmd + ` shutdown" to apply changes.`)
		}
		if key == "network_bridge" && config.NetworkBridge != oldConfig.NetworkBridge {
			// restart docker machine if changed and already running
			scli.EnsureSconVMWithSpinner()
			record, err := scli.Client().GetByID(types.ContainerIDDocker)
			checkCLI(err)
			if record.State == types.ContainerStateStarting || record.State == types.ContainerStateRunning {
				spinner := spinutil.Start("green", "Restarting Docker")
				err = scli.Client().ContainerRestart(record)
				spinner.Stop()
				checkCLI(err)
			}
		}

		return nil
	},
}
