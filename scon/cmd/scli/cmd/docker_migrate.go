package cmd

import (
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/dmigrate"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/spf13/cobra"
)

var (
	//flagFromContext string
	flagImages     bool
	flagContainers bool
	flagVolumes    bool
)

func init() {
	dockerCmd.AddCommand(dockerMigrateCmd)
	//dockerMigrateCmd.Flags().StringVar(&flagFromContext, "from-context", "desktop-linux", "Context to migrate from")
	dockerMigrateCmd.Flags().BoolVar(&flagImages, "images", true, "Migrate images")
	dockerMigrateCmd.Flags().BoolVar(&flagContainers, "containers", true, "Migrate containers")
	dockerMigrateCmd.Flags().BoolVar(&flagVolumes, "volumes", true, "Migrate volumes")
}

var dockerMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate containers, images, and volumes from Docker Desktop",
	Long: `Migrate containers, images, volumes, and other data from Docker Desktop to OrbStack.
`,
	Example: "  " + appid.ShortCtl + " docker migrate",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		// start docker desktop if needed
		err := util.Run("open", "-g" /*don't activate*/, "-b", "com.docker.docker")
		checkCLI(err)

		// TODO wait for start

		// prefer to skip a proxy layer if possible, for perf
		srcSocket := coredir.HomeDir() + "/.docker/run/docker.sock"
		rawDockerSock := coredir.HomeDir() + "/Library/Containers/com.docker.docker/Data/docker.raw.sock"
		if _, err := os.Stat(rawDockerSock); err == nil {
			srcSocket = rawDockerSock
		}

		destSocket := conf.DockerSocket()

		migrator, err := dmigrate.NewMigratorWithUnixSockets(srcSocket, destSocket)
		checkCLI(err)

		err = migrator.MigrateAll(dmigrate.MigrateParams{
			IncludeImages:     flagImages,
			IncludeContainers: flagContainers,
			IncludeVolumes:    flagVolumes,
		})
		checkCLI(err)

		// TODO: if we started docker desktop, quit it
		// err = util.Run("osascript", "-e", `quit app "Docker Desktop"`)
		// checkCLI(err)

		return nil
	},
}
