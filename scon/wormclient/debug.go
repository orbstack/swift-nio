package wormclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/alessio/shellescape"
	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/sshenv"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	pb "github.com/orbstack/macvirt/scon/wormclient/generated"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

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

// registryImage should point to drm server; for locally testing, it's more convenient to just
// spin up a registry and push/pull to that registry instead
// const registryImage = "drmserver.orb.local/wormhole:latest"
const registryImage = "registry.orb.local/wormhole:latest"

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
			err = client.RemoveContainer(c.ID, true)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		}
	}
	if serverContainerId == "" {
		init := true
		// note: start server container with a constant name so that at most one server container exists
		serverContainerId, err = client.RunContainer(dockerclient.RunContainerOptions{Name: "orbstack-wormhole", PullImage: true},
			&dockertypes.ContainerCreateRequest{
				Image:      registryImage,
				Entrypoint: []string{"/wormhole-server"},
				HostConfig: &dockertypes.ContainerHostConfig{
					Privileged:   true,
					Binds:        []string{"wormhole-data:/data"},
					CgroupnsMode: "host",
					PidMode:      "host",
					AutoRemove:   true,
					Init:         &init,
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

	conn, err := client.ExecStream(serverContainerId, &dockertypes.ContainerExecCreateRequest{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/wormhole-proxy"},
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
	err = server.ReadMessage(message)
	if err != nil {
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

func startRemoteWormhole(client *dockerclient.Client, drmToken string, wormholeConfig string) error {
	server, err := connectRemote(client, drmToken, maxRetries)
	if err != nil {
		return err
	}

	var originalState *term.State
	var ptyConfig *pb.PtyConfig

	// see scli/shell/ssh.go
	// TODO: merge into a shared path with ssh.go
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
		ptyConfig = &pb.PtyConfig{
			TermEnv: termEnv,
			Rows:    uint32(h),
			Cols:    uint32(w),
			Termios: serializedTermios,
		}
	}

	// start wormhole-attach payload
	err = server.WriteMessage(&pb.RpcClientMessage{
		ClientMessage: &pb.RpcClientMessage_StartPayload{
			StartPayload: &pb.StartPayload{
				WormholeConfig: string(wormholeConfig),
				PtyConfig:      ptyConfig,
			},
		},
	})
	if err != nil {
		return err
	}

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}

			err = server.WriteMessage(&pb.RpcClientMessage{
				ClientMessage: &pb.RpcClientMessage_StdinData{
					StdinData: &pb.StdinData{Data: buf[:n]},
				},
			})
			if err != nil {
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
				w, h, err := term.GetSize(ptyFd)
				if err != nil {
					return
				}

				err = server.WriteMessage(&pb.RpcClientMessage{
					ClientMessage: &pb.RpcClientMessage_TerminalResize{
						TerminalResize: &pb.TerminalResize{Rows: uint32(h), Cols: uint32(w)},
					},
				})
				if err != nil {
					return
				}
			}
		}
	}()

	for {
		message := &pb.RpcServerMessage{}
		err = server.ReadMessage(message)
		if err != nil {
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

func debugRemote(containerID string, daemon *dockerclient.DockerConnection, drmToken string, flagWorkdir string, args []string) (int, error) {
	if drmToken == "" {
		// todo: explicitly check for pro license as well
		return sshenv.ExitCodeNeedsProLicense, nil
	}

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

	wormholeConfig, err := json.Marshal(&sshtypes.WormholeConfig{
		InitPid:          containerInfo.State.Pid,
		DrmToken:         drmToken,
		ContainerWorkdir: workingDir,
		ContainerEnv:     containerInfo.Config.Env,
		EntryShellCmd:    shellCmd,
	})
	if err != nil {
		return 1, err
	}

	err = startRemoteWormhole(client, drmToken, string(wormholeConfig))
	if err != nil {
		color.New(color.FgRed).Fprintln(os.Stderr, "\nRemote debug session unexpectedly closed")
		// todo: just return an error, checkcli will handle the red-printing and exit code
		return 1, err
	}

	return 0, nil
}

func debugLocal(containerID string, flagWorkdir string, args []string) (int, error) {
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

	// note: non-zero exit codes are not errors, we handle them explicitly in cmd/debug.go
	return exitCode, nil
}

// maybe instead of returning both exitCode and err, just wrap the exitCode in an error object?
// the semantics are that exitCode is the actual exit code of the debug session, err is any error that occurred starting the debug session
func WormholeDebug(containerID string, context string, workdir string, fallback bool, args ...string) (exitCode int, err error) {
	daemon, isLocal, err := GetDaemon(context)
	if err != nil {
		return 1, err
	}

	if isLocal {
		return debugLocal(containerID, workdir, args)
	}

	drmToken, err := GetDrmToken()
	if err != nil {
		return sshenv.ExitCodeNeedsProLicense, err
	}

	return debugRemote(containerID, daemon, drmToken, workdir, args)
}
