package cmd

import (
	"errors"
	"os"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/shell"
	"github.com/spf13/cobra"
)

var (
	useShell     bool
	usePath      bool
	flagWantHelp bool
)

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().BoolVarP(&useShell, "shell", "s", false, "Use the login shell instead of running command directly")
	runCmd.Flags().BoolVarP(&usePath, "path", "p", false, "Translate absolute Linux paths to macOS paths (experimental)")
}

func parseRunFlags(args []string) ([]string, error) {
	inFlag := false
	var lastI int
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
				flagWantHelp = true
				continue
			}

			// 2. look for a pair
			keyPart, valuePart, ok := strings.Cut(arg, "=")
			// if we have a pair, we can also set it and continue
			if ok {
				switch keyPart {
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
	Use:     "run [OPTIONS] -- [COMMAND] [ARGS]...",
	Aliases: []string{"exec", "shell"},
	Short:   "Run command on macOS",
	Long: `Run a command on macOS.

If no arguments are provided, an interactive shell is started.

You can also prefix commands with "mac" to run them on macOS. For example:
    mac uname -a
will run "uname -a" on macOS, and is equivalent to: macctl run uname -a
`,
	Example: "  macctl run ls",
	Args:    cobra.ArbitraryArgs,

	// custom flag parsing - so we don't rely on --
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// parse flags
		var err error
		args, err = parseRunFlags(args)
		if err != nil {
			return err
		}
		if flagWantHelp {
			cmd.Help()
			return nil
		}

		if usePath {
			args = shell.TranslateArgPaths(args)
		}
		if useShell {
			args = []string{shellescape.QuoteCommand(args)}
		}

		exitCode, err := shell.ConnectSSH(shell.CommandOpts{
			CombinedArgs: args,
			UseShell:     useShell,
		})
		if err != nil {
			panic(err)
		}

		os.Exit(exitCode)
		return nil
	},
}
