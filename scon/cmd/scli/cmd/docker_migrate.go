package cmd

import (
	"errors"
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/dmigrate"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/sirupsen/logrus"
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
	dockerMigrateCmd.Flags().BoolVarP(&flagImages, "images", "i", true, "Migrate images")
	dockerMigrateCmd.Flags().BoolVarP(&flagContainers, "containers", "c", true, "Migrate containers")
	dockerMigrateCmd.Flags().BoolVarP(&flagVolumes, "volumes", "v", true, "Migrate volumes")
	dockerMigrateCmd.Flags().StringVarP(&flagFormat, "format", "f", "text", "Output format")
}

var dockerMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate containers, images, and volumes from Docker Desktop",
	Long: `Migrate containers, images, volumes, and other data from Docker Desktop to OrbStack.
`,
	Example: "  " + appid.ShortCmd + " docker migrate",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagFormat == "json" {
			logrus.SetFormatter(&logrus.JSONFormatter{})
		}

		scli.EnsureSconVMWithSpinner()

		logrus.Info("Starting Docker Desktop")
		srcSocket := coredir.HomeDir() + "/.docker/run/docker.sock"
		remoteWasRunning := dmigrate.RemoteIsRunning(srcSocket)

		// start docker desktop if needed
		if !remoteWasRunning {
			err := util.Run("open", "-g" /*don't activate*/, "-j" /*hide*/, "-b", "com.docker.docker")
			checkCLI(err)

			// wait for start
			err = dmigrate.WaitForRemote(srcSocket)
			checkCLI(err)
		}

		// prefer to skip a proxy layer if possible, for perf
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
		if err != nil {
			if flagFormat == "json" && errors.Is(err, dmigrate.ErrEntitiesFailed) {
				// ignore - gui already got json log errors
			} else {
				checkCLI(err)
			}
		}

		// if we started remote, quit it
		if !remoteWasRunning {
			logrus.Info("Stopping Docker Desktop")
			err = util.Run("osascript", "-e", `quit app "Docker Desktop"`)
			checkCLI(err)
		}

		logrus.Info("Done")
		return nil
	},
}
