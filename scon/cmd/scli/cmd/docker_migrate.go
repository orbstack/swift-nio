package cmd

import (
	"errors"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/dmigrate"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	// flagFromContext string
	flagImages     bool
	flagContainers bool
	flagVolumes    bool
)

func init() {
	dockerCmd.AddCommand(dockerMigrateCmd)
	// dockerMigrateCmd.Flags().StringVar(&flagFromContext, "from-context", "desktop-linux", "Context to migrate from")
	dockerMigrateCmd.Flags().BoolVarP(&flagImages, "images", "i", true, "Migrate images")
	dockerMigrateCmd.Flags().BoolVarP(&flagContainers, "containers", "c", true, "Migrate containers")
	dockerMigrateCmd.Flags().BoolVarP(&flagVolumes, "volumes", "v", true, "Migrate volumes")
	dockerMigrateCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Disable filters and migrate everything")
	dockerMigrateCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force migration even if OrbStack already has data")

	// no shorthand, only for gui use
	dockerMigrateCmd.Flags().StringVar(&flagFormat, "format", "text", "Output format")
	dockerMigrateCmd.Flags().MarkHidden("format")
}

// TODO fix code dupe
var dockerMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate containers, volumes, and images from Docker Desktop",
	Long: `Migrate containers, volumes, images, and other data from Docker Desktop to OrbStack.
`,
	Example:           "  " + rootCmd.Use + " docker migrate",
	Args:              cobra.NoArgs,
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagFormat == "json" {
			logrus.SetFormatter(&logrus.JSONFormatter{})
		}

		scli.EnsureSconVMWithSpinner()

		logrus.Info("Starting Docker Desktop")
		srcSocket := conf.DockerRemoteCtxSocket()
		remoteWasRunning := dmigrate.RemoteIsRunning(srcSocket)

		// start docker desktop if needed
		if !remoteWasRunning {
			err := util.Run("open", "-g" /*don't activate*/, "-j" /*hide*/, "-b", "com.docker.docker")
			if err != nil {
				if strings.Contains(err.Error(), "LSCopyApplicationURLsForBundleIdentifier") {
					checkCLI(errors.New("Docker Desktop is not installed"))
				} else {
					checkCLI(err)
				}
			}

			// wait for start
			err = dmigrate.WaitForRemote(srcSocket)
			checkCLI(err)
		}

		destSocket := conf.DockerSocket()

		migrator, err := dmigrate.NewMigratorWithUnixSockets(srcSocket, destSocket)
		checkCLI(err)

		err = migrator.MigrateAll(dmigrate.MigrateParams{
			All: flagAll,

			IncludeImages:     flagImages,
			IncludeContainers: flagContainers,
			IncludeVolumes:    flagVolumes,

			ForceIfExisting: flagForce,
		})
		if err != nil {
			if flagFormat == "json" && errors.Is(err, dmigrate.ErrEntitiesFailed) {
				// ignore - gui already got json log errors
			} else {
				checkCLI(err)
			}
		}

		err = migrator.Finalize()
		checkCLI(err)

		logrus.Info("Done")
		return nil
	},
}
