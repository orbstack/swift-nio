package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/sshenv"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var (
	flagFallback bool
	flagReset    bool
)

func init() {
	rootCmd.AddCommand(debugCmd)
	debugCmd.Flags().StringVarP(&flagWorkdir, "workdir", "w", "", "Set the working directory")
	debugCmd.Flags().BoolVarP(&flagFallback, "fallback", "f", false, "Fallback to 'docker exec' if no Pro license")
	debugCmd.Flags().BoolVar(&flagReset, "reset", false, "Resets Debug Shell data")
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
			case "--reset", "-reset":
				flagReset = true
				return nil, nil, nil
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

type ContainerData struct {
	State struct {
		Pid int
	}
	Config struct {
		WorkingDir string
		Env        []string
	}
}

type ContextData struct {
	Name      string
	Endpoints struct {
		Docker struct {
			Host string
		}
	}
}

type WormholeParams struct {
	Pid        int      `json:"init_pid"`
	Env        []string `json:"container_env"`
	WorkingDir string   `json:"container_workdir"`
	ShellCmd   string   `json:"entry_shell_cmd"`
}

type TermiosParams struct {
	Iflag int     `json:"input_flags"`
	Oflag int     `json:"output_flags"`
	Cflag int     `json:"control_flags"`
	Lflag int     `json:"local_flags"`
	Cc    [32]int `json:"control_chars"`
	// Ispeed int     `json:"c_ispeed"`
	// Ospeed int     `json:"c_ospeed"`
}

func GetTermiosParams() (string, error) {
	// use TIOCGETA on mac instead of TCGETS2 (only on linux)?
	termios, err := unix.IoctlGetTermios(0, unix.TIOCGETA)
	if err != nil {
		return "", err
	}

	var cc [32]int
	for i, val := range termios.Cc {
		cc[i] = int(val)
	}

	termiosParams, err := json.Marshal(TermiosParams{
		Iflag: int(termios.Iflag),
		Oflag: int(termios.Oflag),
		Cflag: int(termios.Cflag),
		Lflag: int(termios.Lflag),
		Cc:    cc,
		// Ispeed: int(termios.Ispeed),
		// Ospeed: int(termios.Ospeed),
	})
	if err != nil {
		return "", err
	}

	return string(termiosParams), nil
}

func startRpcConnection(containerId string, dockerHostEnv []string) error {
	dockerBin := conf.FindXbin("docker")
	// _, err := GetTermiosParams()
	// if err != nil {
	// 	return errors.New("failed to get termios params")
	// }
	fmt.Println("setting raw terminal")
	originalState, err := term.MakeRaw(0)
	// originalState, err := term.GetState(0) // just for debugging so terminal doesn't get annoying
	if err != nil {
		return errors.New("could not set pty to raw mode")
	}
	defer func() {
		fmt.Println("restoring raw terminal")
		term.Restore(0, originalState)
	}()

	cmd := exec.Command(dockerBin, "exec", "-i", containerId, "/wormhole-client")
	cmd.Env = dockerHostEnv

	// cmd.Stderr = os.Stderr
	// cmd.Stdout = os.Stdout

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("could not create stdin pipe")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New("could not create stdout pipe")
	}
	debugFile, err := os.Create("tmp.txt")
	if err != nil {
		return err
	}
	defer debugFile.Close()

	err = WriteTermiosState(stdin)
	if err != nil {
		return err
	}

	fmt.Fprintf(debugFile, "finished writing termios")
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				if err == io.EOF {
					return
				}
				fmt.Printf("read request: %w", err)
				return
			}

			if n > 0 {
				payload, err := SerializeMessage(CreateStdinDataMessage(buf[:n]))
				if err != nil {
					fmt.Printf("could not create rpc stdin message")
					return
				}
				stdin.Write(payload)
			} else {
				fmt.Printf("read 0 bytes???")
			}
		}
	}()

	go func() {
		for {
			rpcResponse, err := DeserializeMessage(stdout)
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}

			fmt.Fprintf(debugFile, "rpc response: %+v\n", rpcResponse)
			if rpcResponse.Type == StdDataType {
				os.Stdout.Write(rpcResponse.Payload)
			} else if rpcResponse.Type == ExitType {
				fmt.Fprintf(debugFile, "exit code %d", rpcResponse.Payload[0])
			}
		}
	}()

	// handle window change
	winchChan := make(chan os.Signal, 1)
	signal.Notify(winchChan, unix.SIGWINCH)

	go func() {
		for {
			select {
			case <-winchChan:
				w, h, err := term.GetSize(0)
				if err != nil {
					fmt.Printf("could not get terminal size", err)
					return
				}
				fmt.Printf("got terminal resize event %d %d\n", w, h)
				rpcMessage, err := CreateTerminalResizeMessage(w, h)
				if err != nil {
					fmt.Printf("could not create rpc resize message %+v\n", err)
					return
				}
				payload, err := SerializeMessage(rpcMessage)
				if err != nil {
					fmt.Printf("could not create serialize rpc resize message")
					return
				}
				stdin.Write(payload)
			}
		}
	}()

	fmt.Println("running docker exec")
	err = cmd.Start()
	if err != nil {
		return errors.New("error when executing starting client")
	}

	if err := cmd.Wait(); err != nil {
		return errors.New("error when waiting client")
	}

	fmt.Println("rpc wormhole connection finished")
	return nil
}

func debugRemote(containerID string) error {
	containerID, context, ok := strings.Cut(containerID, "@")
	dockerBin := conf.FindXbin("docker")

	if !ok {
		return errors.New("invalid remote context " + containerID)
	}

	cmd := exec.Command(dockerBin, "context", "inspect", context)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("failed to inspect context")
	}

	var contextInfo []ContextData
	err = json.Unmarshal(output, &contextInfo)
	if err != nil {
		return errors.New("failed to unmarshal context")
	}

	if len(contextInfo) == 0 {
		return errors.New("no context found")
	}
	dockerHost := contextInfo[0].Endpoints.Docker.Host
	dockerHostEnv := append(os.Environ(), "DOCKER_HOST="+dockerHost)
	fmt.Println("using dockerhost: " + dockerHost)

	cmd = exec.Command(dockerBin, "inspect", containerID)
	cmd.Env = dockerHostEnv
	output, err = cmd.Output()
	if err != nil {
		return errors.New("failed to inspect container " + containerID)
	}
	var containerInfo []ContainerData
	err = json.Unmarshal(output, &containerInfo)
	if err != nil {
		return err
	}

	fmt.Printf("%+v\n", containerInfo)

	REGISTRY_IMAGE := "198.19.249.3:5000/wormhole-rootfs"

	wormholeParams, err := json.Marshal(WormholeParams{
		Pid:        containerInfo[0].State.Pid,
		WorkingDir: containerInfo[0].Config.WorkingDir,
		Env:        containerInfo[0].Config.Env,
		ShellCmd:   "",
	})
	if err != nil {
		return errors.New("failed to serialize wormhole params")
	}

	fmt.Println("wormhole params: " + string(wormholeParams))
	cmd = exec.Command(dockerBin, "run", "-d", "--privileged", "--pid=host", "--net=host", "--cgroupns=host", "-v", "wormhole-data:/data", "-v", "/mnt/host-wormhole-unified:/mnt/wormhole-unified:rw,rshared", REGISTRY_IMAGE, string(wormholeParams))
	cmd.Env = dockerHostEnv
	output, err = cmd.Output()
	if err != nil {
		return errors.New("failed to start remote wormhole container ")
	}

	remoteContainerID := strings.TrimSpace(string(output))
	fmt.Println("remote container id: " + remoteContainerID)

	startRpcConnection(remoteContainerID, dockerHostEnv)
	return nil
}

var debugCmd = &cobra.Command{
	Use:     "debug [flags] -- [COMMAND] [ARGS]...",
	Aliases: []string{"wormhole"},
	Short:   "Debug a Docker container with extra commands",
	Long: `Debug a Docker container, with useful commands and tools that make it easy to debug any container (even minimal, distroless, and read-only containers).

You can also use 'dctl' in the debug shell to install and remove packages.

Pro only: requires an OrbStack Pro license.
`,
	Example: "  " + rootCmd.Use + " debug mysql1",
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
		if (containerIDp == nil && !flagReset) || FlagWantHelp {
			cmd.Help()
			return nil
		}
		if flagReset {
			scli.EnsureSconVMWithSpinner()

			spinner := spinutil.Start("blue", "Resetting Debug Shell data")
			err = scli.Client().WormholeNukeData()
			spinner.Stop()
			if err != nil {
				cmd.SilenceUsage = true
				return err
			}

			fmt.Fprintln(os.Stderr, "Debug Shell data reset!")

			return nil
		}
		containerID := *containerIDp

		if strings.Contains(containerID, "@") {
			// remote debug
			return debugRemote(containerID)
			// return nil
		}

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

		if exitCode == sshenv.ExitCodeNeedsProLicense {
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
		if exitCode == sshenv.ExitCodeNixDebugUnsupported {
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
