package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var (
	flagFallback bool
)

func init() {
	rootCmd.AddCommand(debugCmd)
	debugCmd.Flags().StringVarP(&flagWorkdir, "workdir", "w", "", "Set the working directory")
	debugCmd.Flags().BoolVarP(&flagFallback, "fallback", "f", false, "Fallback to 'docker exec' if no Pro license")
}

func ParseDebugFlags(args []string) ([]string, *string, error) {
	inFlag := false
	lastI := -1 // deal with empty case
	var containerID *string
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
			case "-f", "--fallback", "-fallback":
				flagFallback = true
				continue
			}

			// 2. look for a pair
			keyPart, valuePart, ok := strings.Cut(arg, "=")
			// if we have a pair, we can also set it and continue
			if ok {
				switch keyPart {
				case "-w", "--workdir", "-workdir":
					flagWorkdir = valuePart
				// bools: allow true/false
				case "-f", "--fallback", "-fallback":
					flagFallback = valuePart == "true"
				}
				continue
			}

			// 3. we're at the beginning of a key-value pair. set the flag and wait for the value
			switch keyPart {
			case "-w", "--workdir", "-workdir":
				lastStringFlag = &flagWorkdir
			// don't allow two-part bool
			default:
				return nil, nil, errors.New("unknown flag " + arg)
			}
			inFlag = true
		} else if containerID == nil {
			// we've encountered an argument that's not a flag or a flag value.
			// this first positional arg is the container ID.
			binding := arg // new var binding for pointer
			containerID = &binding
		} else {
			// we've already consumed a positional arg, and this is positional[1].
			// this marks the end of flags
			lastI -= 1 // not consumed
			break
		}
	}

	if inFlag {
		// we're in a flag, but we've reached the end of the args.
		// this is an error
		return nil, nil, errors.New("missing value for flag " + args[lastI])
	}

	// skip the flags and value we got
	return args[lastI+1:], containerID, nil
}

func fallbackDockerExec(containerID string) error {
	// prefer bash, otherwise use sh
	return unix.Exec(conf.FindXbin("docker"), []string{"docker", "--context", "orbstack", "exec", "-it", containerID, "sh", "-c", "command -v bash > /dev/null && exec bash || exec sh"}, os.Environ())
}

var debugCmd = &cobra.Command{
	Use:     "debug [flags] -- [COMMAND] [ARGS]...",
	Aliases: []string{"wormhole"},
	Short:   "Debug a Docker container with extra commands",
	Long: `Debug a Docker container, with useful commands and tools that make it easy to debug any container (even minimal, distroless, and read-only containers).

You can also use 'dctl' in the debug shell to install and remove packages.

Pro only: requires a Pro license for OrbStack.
`,
	Example: "  " + appid.ShortCmd + " debug mysql1",
	Args:    cobra.ArbitraryArgs,

	// custom flag parsing - so we don't rely on --
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// parse flags
		var err error
		args, containerIDp, err := ParseDebugFlags(args)
		if err != nil {
			return err
		}
		if containerIDp == nil || FlagWantHelp {
			cmd.Help()
			return nil
		}
		containerID := *containerIDp

		scli.EnsureSconVMWithSpinner()

		// don't use default (host) workdir,
		// unless this for ovm or docker machine on a debug build
		workdir := ""
		if conf.Debug() {
			if containerID == sshtypes.WormholeIDDocker {
				workdir, err = os.Getwd()
				check(err)
			} else if containerID == sshtypes.WormholeIDHost {
				workdir, err = os.Getwd()
				check(err)
				workdir = mounts.Virtiofs + workdir // includes leading /
			}
		}

		if flagWorkdir != "" {
			workdir = flagWorkdir
		}

		exitCode, err := shell.RunSSH(shell.CommandOpts{
			CombinedArgs:     args,
			ContainerName:    types.ContainerDocker,
			Dir:              &workdir,
			User:             "wormhole:" + containerID,
			WormholeFallback: flagFallback,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n%v\n", err)
			os.Exit(1)
		}

		// 125 = requires Pro
		if exitCode == 125 {
			if flagFallback {
				fmt.Fprintln(os.Stderr, color.New(color.FgBlue).Sprintf(`%s making it easy to debug any container (even minimal/distroless).
It also allows installing over 80,000 packages.

To use Debug Shell, get a Pro license: https://orbstack.dev/pricing

Learn more: https://go.orbstack.dev/debug
`, color.New(color.Bold).Sprint("NEW: OrbStack Debug Shell provides useful commands & tools,")))

				// fallback to docker exec
				err = fallbackDockerExec(containerID)
				checkCLI(err)
			} else {
				fmt.Fprintln(os.Stderr, color.New(color.FgRed).Sprintf(`A Pro license is required to use OrbStack Debug Shell.
%s making it easy to debug any container (even minimal/distroless).
It also allows installing over 80,000 packages.

Learn more: https://go.orbstack.dev/debug
Get a license: https://orbstack.dev/pricing
`, color.New(color.Bold).Sprint("Debug Shell provides useful commands & tools,")))
			}
		}

		// 124 = requested fallback mode, and container is Nix
		if exitCode == 124 {
			fmt.Fprintln(os.Stderr, color.New(color.FgYellow).Sprint(`OrbStack Debug Shell does not yet support Nix containers.
Falling back to 'docker exec'.
Learn more: https://go.orbstack.dev/debug
`))

			// fallback to docker exec
			err = fallbackDockerExec(containerID)
			checkCLI(err)
		}

		os.Exit(exitCode)
		return nil
	},
}
