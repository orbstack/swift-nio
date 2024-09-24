package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	flagArch        string
	flagSetPassword bool
	flagUserData    string
	flagBoxed       bool
)

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().StringVarP(&flagUser, "user", "u", "", "Username for the default user")
	createCmd.Flags().BoolVarP(&flagSetPassword, "set-password", "p", false, "Set a password for the default user")
	createCmd.Flags().StringVarP(&flagArch, "arch", "a", "", "Override the default architecture")
	createCmd.Flags().StringVarP(&flagUserData, "user-data", "c", "", "Path to Cloud-init user data file (for automatic setup)")
	if conf.Debug() {
		createCmd.Flags().BoolVarP(&flagBoxed, "boxed", "b", false, "Create a boxed machine (no integration)")
	}
}

var createCmd = &cobra.Command{
	Use:     "create [flags] DISTRO[:VERSION] [MACHINE_NAME]",
	Aliases: []string{"add", "new"},
	Short:   "Create a new machine",
	Long: `Create a new machine with the specified distribution.

Version is optional; the latest stable version will be used if not specified.
To remove a machine, use "` + appid.ShortCmd + ` delete".

By default, a Linux user will be created with the same name as your macOS user. Use "--user" to change the name, and "--set-password" to set a password for both this user and root.

Supported distros: ` + strings.Join(images.Distros(), "  ") + `
Supported CPU architectures: ` + strings.Join(images.Archs(), "  ") + `
`,
	Example: `  orb create -a arm64 ubuntu:mantic
  orb create -a amd64 fedora foo`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// validate arch
		arch := images.NativeArch()
		if flagArch != "" {
			arch = flagArch
			if !util.SliceContains(images.Archs(), arch) {
				return errors.New("invalid architecture: " + arch)
			}
		}

		// ask for password
		var password string
		if flagSetPassword {
			// prompt for password
			fmt.Print("Password for Linux user: ")
			pwdData, err := term.ReadPassword(int(os.Stdin.Fd()))
			checkCLI(err)
			password = string(pwdData)

			fmt.Println()
		}

		// split distro
		parts := strings.SplitN(args[0], ":", 2)
		distro := parts[0]
		image, ok := images.DistroToImage[distro]
		if !ok {
			return errors.New("invalid distro: " + distro)
		}
		version := images.ImageToLatestVersion[image]
		if len(parts) > 1 {
			version = parts[1]
		}

		// determine name
		name := distro
		if len(args) > 1 {
			name = args[1]
		}

		// read cloud init file
		var userData string
		if flagUserData != "" {
			data, err := os.ReadFile(flagUserData)
			checkCLI(err)
			userData = string(data)
		}

		// spinner
		scli.EnsureSconVMWithSpinner()
		spinner := spinutil.Start("blue", "Creating "+name)
		_, err := scli.Client().Create(types.CreateRequest{
			Name: name,
			Image: types.ImageSpec{
				Distro:  image,
				Version: version,
				Arch:    arch,
			},
			Config: types.MachineConfig{
				Isolated:        flagBoxed,
				DefaultUsername: flagUser,
			},
			UserPassword:      password,
			CloudInitUserData: userData,
		})
		spinner.Stop()
		if err != nil {
			// print to stderr
			cmd.PrintErrln(err)
			os.Exit(1)
		}

		return nil
	},
}
