package cmd

import (
	"fmt"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(dockerCmd)
}

var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Show commands for using Docker",
	Long: `Show commands and instructions for using Docker.
This includes building and running Docker containers, as well as using Docker Compose.
`,
	Example: "  " + appid.ShortCtl + " ssh",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		fmt.Print(strings.Replace(`The "orb" and "orbctl" commands are for managing OrbStack and full Linux machines.

To build and run Docker containers and manage anything related to Docker, use the "docker" command directly from macOS:
    docker
There is no need to use Linux machines if you only want to use Docker.

For example, to run an example container:
    docker run -it -p 80:80 docker/getting-started
Then visit http://localhost in your browser.

Docker Compose and buildx are also included:
    docker compose
    docker buildx
`, "<HOST>", appid.AppName, -1))

		return nil
	},
}
