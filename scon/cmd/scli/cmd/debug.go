package cmd

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
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

	pb "github.com/orbstack/macvirt/scon/cmd/scli/generated"
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

const maxRetries = 3

// drm server
// const registryImage = "198.19.249.3:5000/wormhole-server:latest"
// const registryImage = "host.orb.internal:8400/wormhole:latest"
// const registryImage = "localhost:5000/wormhole:latest"

const registryImage = "host.orb.internal:5000/wormhole:latest"

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

func connectRemote(client *dockerclient.Client, drmToken string, retries int) (*RpcServer, error) {
	// Start wormhole server (if not running) and establish a client connection. There are a few scenarios where a race can occur:
	//   - two clients start a server container at the same time, resulting in a name conflict. In this case,
	// the process that experiences the name conflict will retry.
	//   - server container shuts down before we `docker exec client` into it. Retry.
	//   - client connects right before the server shuts down. We detect this if we receive an EOF from the server
	// before we receive an initial ACK message, and retry in this case.

	if retries == 0 {
		// we should never actually reach the base case here because we directly return err when retries drops to 1
		return nil, errors.New("failed to start debug session")
	}

	containers, err := client.ListContainers(true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	var serverContainerId string = ""
	for _, c := range containers {
		// container name is prepended with an extra forward slash
		if c.Names[0] == "/orbstack-wormhole" {
			if c.State == "running" {
				serverContainerId = c.ID
				continue
			}

			// remove the server container if it's not running
			if err := client.RemoveContainer(c.ID, true); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		}
	}
	if serverContainerId == "" {
		// note: start server container with a constant name so that at most one server container exists
		serverContainerId, err = client.RunContainer(dockerclient.RunContainerOptions{Name: "orbstack-wormhole", PullImage: true},
			&dockertypes.ContainerCreateRequest{
				Image: registryImage,
				HostConfig: &dockertypes.ContainerHostConfig{
					Privileged:   true,
					Binds:        []string{"wormhole-data:/data", "/mnt/host-wormhole-unified:/mnt/wormhole-unified:rw,rshared"},
					CgroupnsMode: "host",
					PidMode:      "host",
					NetworkMode:  "host",
					AutoRemove:   true,
				},
			})
		if err != nil {
			// potential name conflict (two servers started at the same time), retry
			if retries == 1 {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			return connectRemote(client, drmToken, retries-1)
		}
	}

	conn, err := client.InteractiveExec(serverContainerId, &dockertypes.ContainerExecCreateRequest{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/wormhole-client"},
	})
	if err != nil {
		// server container shuts down before we could `docker exec client` into it, retry
		if retries == 1 {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return connectRemote(client, drmToken, retries-1)
	}

	demuxReader, demuxWriter := io.Pipe()
	go func() {
		defer demuxReader.Close()
		defer demuxWriter.Close()
		defer conn.Close()
		dockerclient.DemuxOutput(conn, demuxWriter)
	}()

	sessionStdin := conn
	sessionStdout := demuxReader

	server := RpcServer{reader: sessionStdout, writer: sessionStdin}

	// wait for server to acknowledge client.
	message := &pb.RpcServerMessage{}
	if err := server.ReadMessage(message); err != nil {
		// EOF means that the client attach session was abruptly closed. This may happen
		// if the `docker exec client` connects to the server container right before the
		// container is about to shut down. Retry.
		if err == io.EOF {
			if retries == 1 {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			return connectRemote(client, drmToken, retries-1)
		}
		return nil, err
	}
	switch message.ServerMessage.(type) {
	case *pb.RpcServerMessage_ClientConnectAck:
		// at this point, the server has incremented the connection refcount and we can safely continue
		break
	default:
		return nil, errors.New("could not connect to wormhole server")
	}

	return &server, nil
}

func startRemoteWormhole(client *dockerclient.Client, drmToken string, wormholeParam string) error {
	server, err := connectRemote(client, drmToken, maxRetries)
	if err != nil {
		return err
	}

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
			originalState, err = term.MakeRaw(ptyFd)
			if err != nil {
				return err
			}
			defer term.Restore(ptyFd, originalState)
		}

		serializedTermios, err := SerializeTermios(termios)
		if err != nil {
			return err
		}

		// request pty
		if err := server.WriteMessage(&pb.RpcClientMessage{
			ClientMessage: &pb.RpcClientMessage_RequestPty{
				RequestPty: &pb.RequestPty{
					TermEnv: termEnv,
					Rows:    uint32(h),
					Cols:    uint32(w),
					Termios: serializedTermios,
				},
			},
		}); err != nil {
			return err
		}
	}

	// start wormhole-attach payload
	if err := server.WriteMessage(&pb.RpcClientMessage{
		ClientMessage: &pb.RpcClientMessage_StartPayload{
			StartPayload: &pb.StartPayload{WormholeParam: string(wormholeParam)},
		},
	}); err != nil {
		return err
	}

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}

			if err := server.WriteMessage(&pb.RpcClientMessage{
				ClientMessage: &pb.RpcClientMessage_StdinData{
					StdinData: &pb.StdinData{Data: buf[:n]},
				},
			}); err != nil {
				return
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
					return
				}

				if err := server.WriteMessage(&pb.RpcClientMessage{
					ClientMessage: &pb.RpcClientMessage_TerminalResize{
						TerminalResize: &pb.TerminalResize{Rows: uint32(h), Cols: uint32(w)},
					},
				}); err != nil {
					return
				}
			}
		}
	}()

	for {
		message := &pb.RpcServerMessage{}
		if err := server.ReadMessage(message); err != nil {
			if err == io.EOF {
				return err
			}
			return err
		}

		switch v := message.ServerMessage.(type) {
		case *pb.RpcServerMessage_StdoutData:
			os.Stdout.Write(v.StdoutData.Data)
		case *pb.RpcServerMessage_StderrData:
			os.Stderr.Write(v.StderrData.Data)
		case *pb.RpcServerMessage_ExitStatus:
			term.Restore(ptyFd, originalState)
			os.Exit(int(v.ExitStatus.ExitCode))
		}
	}
}

func debugRemote(containerID string, daemon *dockerclient.DockerConnection, drmToken string, args []string) error {
	client, err := dockerclient.NewClientWithDrmAuth(daemon, drmToken)
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

	wormholeParam, err := json.Marshal(WormholeRemoteServerParams{
		InitPid:          containerInfo.State.Pid,
		ContainerWorkdir: workingDir,
		ContainerEnv:     containerInfo.Config.Env,
		EntryShellCmd:    shellCmd,
		DrmToken:         drmToken,
	})
	if err != nil {
		return err
	}

	if err := startRemoteWormhole(client, drmToken, string(wormholeParam)); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
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

func nukeRemoteData(cmd *cobra.Command, daemon *dockerclient.DockerConnection, drmToken string) error {
	spinner := spinutil.Start("blue", "Resetting (Remote) Debug Shell data")
	client, err := dockerclient.NewClientWithDrmAuth(daemon, drmToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	server, err := connectRemote(client, drmToken, maxRetries)
	if err != nil {
		return err
	}

	// todo: with rpc, directly send NukeData request and get response back
	server.WriteMessage(&pb.RpcClientMessage{
		ClientMessage: &pb.RpcClientMessage_NukeData{},
	})
	message := &pb.RpcServerMessage{}
	if err := server.ReadMessage(message); err != nil {
		return err
	}
	var exitCode int
	switch v := message.ServerMessage.(type) {
	case *pb.RpcServerMessage_ExitStatus:
		exitCode = int(v.ExitStatus.ExitCode)
	}

	spinner.Stop()

	if exitCode == 1 {
		fmt.Fprintf(os.Stderr, "Please exit all Debug Shell sessions before using this command.")
		os.Exit(1)
	}

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

		// Use the explict context if specified (e.g. `orb debug container@mycontext`),
		// otherwise fallback to standard docker context preference order.
		var context string
		var containerID string
		if containerIDp != nil {
			if flagReset {
				// handle explicit context reset (orb debug --reset mycontext)
				context = *containerIDp
			} else {
				// split container param by @ (e.g. `orb debug mycontainer@mycontext`). If
				// *containerIDp does not contain @, then context will be set to ""
				containerID, context, _ = strings.Cut(*containerIDp, "@")
			}
		}
		if context == "" {
			context = dockerclient.GetCurrentContext()
		}

		daemon, err := dockerclient.GetDockerDaemon(context)
		if err != nil {
			return err
		}

		// check if the context is the local orbstack context
		isLocal := false
		if orbContext, err := dockerclient.GetDockerDaemon("orbstack"); err == nil {
			isLocal = orbContext.Host == daemon.Host
		}

		if flagReset {
			if isLocal {
				err = nukeLocalData(cmd)
			} else {
				err = nukeRemoteData(cmd, daemon, keychainState.EntitlementToken)
			}
			if err != nil {
				return err
			}
			return nil
		}

		if containerIDp == nil || FlagWantHelp {
			cmd.Help()
			return nil
		}

		if isLocal {
			debugLocal(containerID, args)
		} else {
			debugRemote(containerID, daemon, keychainState.EntitlementToken, args)
		}

		return nil
	},
}
