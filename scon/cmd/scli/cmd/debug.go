package cmd

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/sshenv"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var (
	flagFallback bool
	flagReset    bool
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

const registryImage = "198.19.249.3:5000/wormhole-rootfs:latest"

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

type WormholeRemoteServerParams struct {
	InitPid          int      `json:"a"`
	DrmToken         string   `json:"e"`
	ContainerWorkdir string   `json:"f"`
	ContainerEnv     []string `json:"g"`
	EntryShellCmd    string   `json:"h"`
	IsLocal          bool     `json:"i"`
}

func WriteTermEnv(writer io.Writer, term string) error {
	if err := binary.Write(writer, binary.BigEndian, uint32(len(term))); err != nil {
		return err
	}
	if _, err := writer.Write([]byte(term)); err != nil {
		return err
	}

	return nil
}

func startRpcConnection(client *dockerclient.Client, containerID string) error {
	conn, err := client.InteractiveExec(containerID, &dockertypes.ContainerExecCreateRequest{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/wormhole-client"},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	demuxReader, demuxWriter := io.Pipe()
	go func() {
		defer demuxReader.Close()
		defer demuxWriter.Close()
		dockerclient.DemuxOutput(conn, demuxWriter)
	}()

	sessionStdin := conn
	sessionStdout := demuxReader
	server := RpcServer{reader: sessionStdout, writer: sessionStdin}

	var originalState *term.State

	// see scli/shell/ssh.go
	ptyFd := -1
	ptyStdin, ptyStdout, ptyStderr := false, false, false
	if term.IsTerminal(fdStdin) {
		ptyStdin = true
		ptyFd = fdStdin
	}
	if term.IsTerminal(fdStdout) {
		ptyStdout = true
		ptyFd = fdStdout
	}
	if term.IsTerminal(fdStderr) {
		ptyStderr = true
		ptyFd = fdStderr
	}
	// need a pty?
	if ptyStdin || ptyStdout || ptyStderr {
		termEnv := os.Getenv("TERM")
		w, h, err := term.GetSize(ptyFd)
		if err != nil {
			return err
		}

		// snapshot the flags
		termios, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
		if err != nil {
			return errors.New("failed to get termios params")
		}

		// raw mode if both outputs are ptys
		if ptyStdout && ptyStderr {
			// fmt.Println("setting raw mode")
			originalState, err = term.MakeRaw(ptyFd)
			if err != nil {
				return err
			}
			defer term.Restore(ptyFd, originalState)
		}

		// request pty
		err = server.RpcRequestPty(termEnv, h, w, termios)
		if err != nil {
			return err
		}
	}

	// start wormhole-attach payload
	if err := server.RpcStart(); err != nil {
		return err
	}

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				// todo: some special handling for errors?
				if err == io.EOF {
					return
				}
				return
			}

			if err := server.RpcWriteStdin(buf[:n]); err != nil {
				return
			}
		}
	}()

	go func() {
		for {
			rpcType, data, err := server.RpcRead()
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}

			switch rpcType {
			case ReadStdioType:
				if data[0] == 1 {
					os.Stdout.Write(data)
				} else if data[0] == 2 {
					os.Stderr.Write(data)
				} else {
					// return
				}
			case ExitCodeType:
				term.Restore(0, originalState)
				os.Exit(int(data[0]))
			}
		}
	}()

	// handle window change
	winchChan := make(chan os.Signal, 1)
	signal.Notify(winchChan, unix.SIGWINCH)

	// run repeatedly until we receive an exit code.. which calls os.Exit
	for {
		select {
		case <-winchChan:
			w, h, err := term.GetSize(0)
			if err != nil {
				return err
			}
			if err := server.RpcWindowChange(h, w); err != nil {
				return err
			}
		}
	}
}

func debugRemote(containerID string, daemon *dockerclient.DockerConnection, drmToken string, args []string) error {
	client, err := dockerclient.NewClient(daemon)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	containerInfo, err := client.InspectContainer(containerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// remote debug does not yet support running of stopped containers
	if !containerInfo.State.Running {
		fmt.Fprintf(os.Stderr, "container %s is not running\n", containerID)
		os.Exit(1)
	}

	workingDir := containerInfo.Config.WorkingDir
	shellCmd := ""

	if flagWorkdir != "" {
		workingDir = flagWorkdir
	}

	if len(args) > 0 {
		shellCmd = shellescape.QuoteCommand(args)
	}

	wormholeParams, err := json.Marshal(WormholeRemoteServerParams{
		IsLocal:          false,
		InitPid:          containerInfo.State.Pid,
		ContainerWorkdir: workingDir,
		ContainerEnv:     containerInfo.Config.Env,
		EntryShellCmd:    shellCmd,
		DrmToken:         drmToken,
	})
	if err != nil {
		return err
	}

	remoteContainerID, err := client.RunContainer(&dockertypes.ContainerCreateRequest{
		Image: registryImage,
		HostConfig: &dockertypes.ContainerHostConfig{
			Privileged:   true,
			Binds:        []string{"wormhole-data:/data", "/mnt/host-wormhole-unified:/mnt/wormhole-unified:rw,rshared"},
			CgroupnsMode: "host",
			PidMode:      "host",
			NetworkMode:  "host",
		},
		Cmd: []string{string(wormholeParams)},
	}, false)

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	startRpcConnection(client, remoteContainerID)
	return nil
}

func debugLocal(containerID string, args []string) error {
	var err error

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
}

func nukeLocalData(cmd *cobra.Command) error {
	scli.EnsureSconVMWithSpinner()

	spinner := spinutil.Start("blue", "Resetting Debug Shell data")
	err := scli.Client().WormholeNukeData()
	spinner.Stop()
	if err != nil {
		cmd.SilenceUsage = true
		return err
	}

	fmt.Fprintln(os.Stderr, "Debug Shell data reset!")
	return nil
}

func nukeRemoteData(cmd *cobra.Command, daemon *dockerclient.DockerConnection) error {
	spinner := spinutil.Start("blue", "Resetting (Remote) Debug Shell data")
	client, err := dockerclient.NewClient(daemon)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	remoteContainerID, err := client.RunContainer(&dockertypes.ContainerCreateRequest{
		Image: registryImage,
		HostConfig: &dockertypes.ContainerHostConfig{
			Privileged: true,
			Binds:      []string{"wormhole-data:/data"},
			// CgroupnsMode: "host",
			// PidMode:      "host",
			// NetworkMode:  "host",
		},
		Cmd: []string{string("--nuke")},
	}, false)

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	containerInfo, err := client.InspectContainer(remoteContainerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%+v\n", containerInfo)

	spinner.Stop()
	if err != nil {
		cmd.SilenceUsage = true
		return err
	}

	fmt.Fprintln(os.Stderr, "(Remote) Debug Shell data reset!")

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

		keychainData, err := drmcore.ReadKeychainDrmState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not get DRM token")
			os.Exit(1)
		}

		var keychainState drmtypes.PersistentState
		err = json.Unmarshal(keychainData, &keychainState)
		if err != nil {
			return err
		}

		// Read the docker daemon address in the following order:
		// 1. Host specified by context in command `orb debug container@context` (overriden below)
		// 2. DOCKER_CONTEXT (overrides DOCKER_HOST)
		// 3. DOCKER_HOST env
		// 4. Host specified by currentContext in `~/.docker/config.json`
		// 5. "default" context (unix:///var/run/docker.sock)
		daemon, err := dockerclient.GetCurrentContext()
		if err != nil {
			return err
		}

		if context := os.Getenv("DOCKER_CONTEXT"); context != "" {
			if daemon, err = dockerclient.GetContext(context); err != nil {
				return err
			}
		} else {
			if hostOverride := os.Getenv("DOCKER_HOST"); hostOverride != "" {
				daemon.Host = hostOverride
			}
			if path := os.Getenv("DOCKER_CERT"); path != "" {
				daemon.TLSData = &dockerclient.TLSData{
					CA:   filepath.Join(path, "ca.pem"),
					Key:  filepath.Join(path, "key.pem"),
					Cert: filepath.Join(path, "cert.pem"),
				}
			}
			if tlsVerify := os.Getenv("TLS_VERIFY"); tlsVerify != "" {
				if tlsVerify == "1" {
					daemon.SkipTLSVerify = false
				} else if tlsVerify == "0" {
					daemon.SkipTLSVerify = true
				}
			}
		}

		if flagReset {
			// the context was explicitly passed as a param (orb debug remote --reset)
			if containerIDp != nil {
				daemon, err = dockerclient.GetContext(*containerIDp)
				if err != nil {
					return err
				}
			}

			orbContext, err := dockerclient.GetContext("orbstack")
			isLocal := err == nil && orbContext.Host == daemon.Host
			if isLocal {
				err = nukeLocalData(cmd)
			} else {
				err = nukeRemoteData(cmd, daemon)
			}
			if err != nil {
				return err
			}
			return nil
		}

		if (containerIDp == nil && !flagReset) || FlagWantHelp {
			cmd.Help()
			return nil
		}
		containerID := *containerIDp

		// explicit docker context overrides any context set via environment variables
		if containerIDp != nil && strings.Contains(*containerIDp, "@") {
			var context string
			var ok bool

			containerID, context, ok = strings.Cut(containerID, "@")
			if !ok {
				fmt.Fprintln(os.Stderr, "Could not parse docker context")
				os.Exit(1)
			}

			if daemon, err = dockerclient.GetContext(context); err != nil {
				return err
			}
		}

		if orbContext, err := dockerclient.GetContext("orbstack"); err == nil && orbContext.Host == daemon.Host {
			debugLocal(containerID, args)
		} else {
			debugRemote(containerID, daemon, keychainState.EntitlementToken, args)
		}

		return nil
	},
}
