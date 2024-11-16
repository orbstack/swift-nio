package wormclient

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/alessio/shellescape"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/sshenv"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
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

const bufSize = 65536

func startRemoteWormhole(client *dockerclient.Client, drmToken string, wormholeConfig string) (int, error) {
	server, err := connectRemote(client, drmToken, maxRetries)
	if err != nil {
		return 1, err
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
			return 1, err
		}

		// snapshot the flags
		termios, err := unix.IoctlGetTermios(ptyFd, unix.TIOCGETA)
		if err != nil {
			return 1, fmt.Errorf("failed to get termios params: %w", err)
		}

		// raw mode if both outputs are ptys
		if ptyStdout && ptyStderr {
			originalState, err = term.MakeRaw(ptyFd)
			if err != nil {
				return 1, err
			}
			defer term.Restore(ptyFd, originalState)
		}

		serializedTermios, err := SerializeTermios(termios)
		if err != nil {
			return 1, err
		}

		// request pty
		ptyConfig = &pb.PtyConfig{
			TermEnv:          termEnv,
			SshConnectionEnv: os.Getenv("SSH_CONNECTION"),
			SshAuthSockEnv:   os.Getenv("SSH_AUTH_SOCK"),
			Rows:             uint32(h),
			Cols:             uint32(w),
			Termios:          serializedTermios,
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
		return 1, err
	}

	go func() {
		buf := make([]byte, bufSize)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				// todo: notify server that stdin has been closed?
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
			return 1, fmt.Errorf("failed to read from server: %w", err)
		}

		switch v := message.ServerMessage.(type) {
		case *pb.RpcServerMessage_StdoutData:
			os.Stdout.Write(v.StdoutData.Data)
		case *pb.RpcServerMessage_StderrData:
			os.Stderr.Write(v.StderrData.Data)
		case *pb.RpcServerMessage_ExitStatus:
			return int(v.ExitStatus.ExitCode), nil
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
		return 1, fmt.Errorf("failed to create docker client: %w", err)
	}

	containerInfo, err := client.InspectContainer(containerID)
	if err != nil {
		return 1, fmt.Errorf("failed to inspect container: %w", err)
	}

	// remote debug does not yet support running of stopped containers
	if !containerInfo.State.Running {
		return 1, fmt.Errorf("container %s is not running", containerID)
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

	return startRemoteWormhole(client, drmToken, string(wormholeConfig))
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
		return 1, err
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
