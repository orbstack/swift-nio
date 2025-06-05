package cmd

import (
	"fmt"
	"slices"

	"github.com/orbstack/macvirt/scon/cmd/scli/cliutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/completions"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configGetCmd)
}

var configGetCmd = &cobra.Command{
	Use:   "get KEY",
	Short: "Get a config value",
	Long: `Get the value of a single OrbStack configuration option.

See "` + rootCmd.Use + ` config show" for a list of options.
`,
	Example:           "  " + rootCmd.Use + " get app.start_at_login",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completions.ConfigKeys,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		configMap, err := cliutil.GetSyntheticVmConfig()
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
