package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
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
	Example:           "  " + rootCmd.Use + " show",
	Args:              cobra.NoArgs,
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()
		config, err := vmclient.Client().GetConfig()
		checkCLI(err)

		// serialize to json
		jsonData, err := json.MarshalIndent(config, "", "  ")
		checkCLI(err)

		// deserialize to map
		var configMap map[string]any
		err = json.Unmarshal(jsonData, &configMap)
		checkCLI(err)

		// add synthetic configs
		guiExe, err := conf.FindGUIExe()
		checkCLI(err)
		out, err := util.RunWithOutput(guiExe, "get-launch-at-login")
		checkCLI(err)
		configMap["app.start_at_login"] = strings.TrimSpace(out) == "true"

		// add machine configs
		containers, err := scli.Client().ListContainers()
		checkCLI(err)
		for _, container := range containers {
			namePart := container.Record.Name
			if strings.ContainsRune(namePart, '.') {
				namePart = "\"" + namePart + "\""
			}
			configMap["machine."+namePart+".username"] = container.Record.Config.DefaultUsername
		}

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
