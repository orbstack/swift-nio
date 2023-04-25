package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/sshpath"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/shell"
	"github.com/spf13/cobra"
)

var (
	useShell     bool
	usePath      bool
	flagMachine  string
	flagUser     string
	FlagWantHelp bool
)

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVarP(&flagMachine, "machine", "m", "", "Use a specific machine")
	runCmd.Flags().StringVarP(&flagUser, "user", "u", "", "Run as a specific user")
	runCmd.Flags().BoolVarP(&useShell, "shell", "s", false, "Use the login shell instead of running command directly")
	runCmd.Flags().BoolVarP(&usePath, "path", "p", false, "Translate absolute macOS paths to Linux paths")
}

func ParseRunFlags(args []string) ([]string, error) {
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
			case "-s", "--shell", "-shell":
				useShell = true
				continue
			case "-p", "--path", "-path":
				usePath = true
				continue
			case "-h", "--help", "-help":
				FlagWantHelp = true
				continue
			}

			// 2. look for a pair
			keyPart, valuePart, ok := strings.Cut(arg, "=")
			// if we have a pair, we can also set it and continue
			if ok {
				switch keyPart {
				case "-m", "--machine", "-machine":
					flagMachine = valuePart
				case "-u", "--user", "-user":
					flagUser = valuePart
				// bools: allow true/false
				case "-s", "--shell", "-shell":
					useShell = valuePart == "true"
				case "-p", "--path", "-path":
					usePath = valuePart == "true"
				}
				continue
			}

			// 3. we're at the beginning of a key-value pair. set the flag and wait for the value
			switch keyPart {
			case "-m", "--machine", "-machine":
				lastStringFlag = &flagMachine
			case "-u", "--user", "-user":
				lastStringFlag = &flagUser
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

var runCmd = &cobra.Command{
	Use:     "run [flags] -- [COMMAND] [ARGS]...",
	Aliases: []string{"exec", "shell"},
	Short:   "Run command on Linux",
	Long: `Run a command on Linux.

If no arguments are provided, an interactive shell is started.
The default machine and/or user are used if not specified.

You can also prefix commands with "` + appid.ShortCmd + `" to run them on Linux. For example:
    ` + appid.ShortCmd + ` uname -a
will run "uname -a" on Linux, and is equivalent to: ` + appid.ShortCtl + ` run uname -a

If you prefer SSH, use "` + appid.ShortCtl + ` ssh" for details.

To run a command on macOS from Linux, use "macctl run" instead.

To pass environment variables, set ORBENV to a colon-separated list of variables:
	ORBENV=EDITOR:VISUAL ` + appid.ShortCmd + ` git commit

Paths are translated automatically when safe.
To be explicit, prefix Linux paths with /mnt/linux and macOS paths with /mnt/mac.
`,
	Example: "  " + appid.ShortCtl + " run ls",
	Args:    cobra.ArbitraryArgs,

	// custom flag parsing - so we don't rely on --
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// parse flags
		var err error
		args, err = ParseRunFlags(args)
		if err != nil {
			return err
		}
		if FlagWantHelp {
			cmd.Help()
			return nil
		}

		scli.EnsureSconVMWithSpinner()

		containerName := flagMachine
		if containerName == "" {
			c, err := scli.Client().GetDefaultContainer()
			checkCLI(err)
			containerName = c.Name
		}

		if usePath {
			args = sshpath.TranslateArgs(args, sshpath.ToLinux, sshpath.ToLinuxOptions{
				TargetContainer: containerName,
			})
		}
		if useShell {
			args = []string{shellescape.QuoteCommand(args)}
		}

		exitCode, err := shell.RunSSH(shell.CommandOpts{
			CombinedArgs:  args,
			UseShell:      useShell,
			ContainerName: containerName,
			User:          flagUser,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n%v\n", err)
			os.Exit(1)
		}

		os.Exit(exitCode)
		return nil
	},
}
