package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(debugCmd)
	debugCmd.Flags().StringVarP(&flagWorkdir, "workdir", "w", "", "Set the working directory")
}

func ParseDebugFlags(args []string) ([]string, error) {
	inFlag := false
	lastI := -1 // deal with empty case
	var lastStringFlag *string
	var arg string
	for lastI, arg = range args {
		if inFlag {
			// we're in a flag. this is the value
			// this is highest priority for cases like "--machine -ubuntu" where machine = "-ubuntu"
			*lastStringFlag = arg
			inFlag = false
		} else if strings.HasPrefix(arg, "-") {
			// this is a flag. either bool, beginning of a key-value, or a key-value pair

			// 1. simple case: if this is a bool flag, set it and continue
			switch arg {
			case "-h", "--help", "-help":
				FlagWantHelp = true
				continue
			}

			// 2. look for a pair
			keyPart, valuePart, ok := strings.Cut(arg, "=")
			// if we have a pair, we can also set it and continue
			if ok {
				switch keyPart {
				case "-w", "--workdir", "-workdir":
					flagWorkdir = valuePart
				}
				continue
			}

			// 3. we're at the beginning of a key-value pair. set the flag and wait for the value
			switch keyPart {
			case "-w", "--workdir", "-workdir":
				lastStringFlag = &flagWorkdir
			// don't allow two-part bool
			default:
				return nil, errors.New("unknown flag " + arg)
			}
			inFlag = true
		} else {
			// we've encountered an argument that's not a flag or a flag value.
			// this is the end of the flags, so we can stop parsing
			lastI -= 1 // not consumed
			break
		}
	}

	if inFlag {
		// we're in a flag, but we've reached the end of the args.
		// this is an error
		return nil, errors.New("missing value for flag " + args[lastI])
	}

	// skip the flags and value we got
	return args[lastI+1:], nil
}

var debugCmd = &cobra.Command{
	Use:     "debug [flags] -- [COMMAND] [ARGS]...",
	Aliases: []string{"wormhole"},
	Short:   "Debug a Docker container",
	Long: `Debug a Docker container.
`,
	Example: "  " + appid.ShortCmd + " debug mysql1",
	Args:    cobra.ArbitraryArgs,

	// custom flag parsing - so we don't rely on --
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// parse flags
		var err error
		args, err = ParseDebugFlags(args)
		if err != nil {
			return err
		}
		if len(args) == 0 || FlagWantHelp {
			cmd.Help()
			return nil
		}
		containerID := args[0]

		scli.EnsureSconVMWithSpinner()

		var workdir *string
		if flagWorkdir != "" {
			workdir = &flagWorkdir
		}

		exitCode, err := shell.RunSSH(shell.CommandOpts{
			CombinedArgs:  args[1:],
			ContainerName: types.ContainerDocker,
			Dir:           workdir,
			User:          "wormhole:" + containerID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n%v\n", err)
			os.Exit(1)
		}

		os.Exit(exitCode)
		return nil
	},
}
