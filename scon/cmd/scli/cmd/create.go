package cmd

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	flagArch        string
	flagSetPassword bool
)

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().BoolVarP(&flagSetPassword, "set-password", "p", false, "Set a password for the default user")
	createCmd.Flags().StringVarP(&flagArch, "arch", "a", "", "Override the default architecture")
}

var createCmd = &cobra.Command{
	Use:     "create [OPTIONS] DISTRO[:VERSION] [CONTAINER_NAME]",
	Aliases: []string{"add", "new"},
	Short:   "Create a new Linux container",
	Long: `Create a new Linux container with the specified distribution.

Version is optional; the latest stable version will be used if not specified.
To remove a container, use "` + appid.ShortCtl + ` delete".

A matching Linux user will be created for your macOS user. Pass "--set-password" to set a password for this Linux user, as well as the root user.

Supported distros: ` + strings.Join(images.Distros(), ", ") + `
Supported CPU architectures: ` + strings.Join(images.Archs(), ", ") + `
`,
	Example: "  " + appid.ShortCtl + " create ubuntu:kinetic -a arm64",
	Args:    cobra.RangeArgs(1, 2),
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
		var password *string
		if flagSetPassword {
			// prompt for password
			pwdData, err := term.ReadPassword(int(os.Stdin.Fd()))
			check(err)
			str := string(pwdData)
			password = &str
		}

		// split distro
		parts := strings.SplitN(args[0], ":", 2)
		distro := parts[0]
		image, ok := images.DistroToImage[distro]
		if !ok {
			return errors.New("invalid distro: " + distro)
		}
		version := images.DistroToLatestVersion[distro]
		if len(parts) > 1 {
			version = parts[1]
		}

		// determine name
		name := distro
		if len(args) > 1 {
			name = args[1]
		}

		// spinner
		spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
		spin.Color("blue")
		spin.Suffix = " Creating " + name
		spin.Start()

		_, err := scli.Client().Create(types.CreateRequest{
			Name: name,
			Image: types.ImageSpec{
				Distro:  image,
				Version: version,
				Arch:    arch,
			},
			UserPassword: password,
		})
		spin.Stop()
		if err != nil {
			// print to stderr
			cmd.PrintErrln(err)
			os.Exit(1)
		}

		return nil
	},
}
