package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unsafe"

	"github.com/gliderlabs/ssh"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/sshenv"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const wormholeImageWriteWarning = `WARNING: You are debugging an image, not a container.
Images are read-only. You can make changes in this session, but they will NOT be saved.

`

const wormholeImageDiffWarning = `

Image changes have been discarded.
`

const wormholeContainerWriteWarning = `WARNING: Support for containerd image store is experimental.
You can debug stopped containers, but saving changes is NOT yet supported.

`

const wormholeContainerDiffWarning = `

Container changes have been discarded.
`

func ptyWarning(isPty bool, message string) string {
	if isPty {
		// for PTY: wrap with yellow escape codes, and translate \n to \r\n for raw mode pty
		return "\033[33m" + strings.ReplaceAll(message, "\n", "\r\n") + "\033[0m"
	} else {
		return message
	}
}

func handleFanotify(fanFile *os.File, event *unix.FanotifyEventMetadata, accessCb func()) (bool, error) {
	defer unix.Close(int(event.Fd))

	// works for both
	isDocker := false
	if event.Mask&unix.FAN_OPEN_PERM != 0 {
		// if requester is dockerd, call access callback

		// read cmdline of pid
		// no way to do this non-racily: pidfd doesn't give us /proc/pid/cmdline
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", event.Pid))
		if err != nil {
			logrus.WithError(err).Error("failed to read pid cmdline")
			return false, nil
		}

		if bytes.HasPrefix(cmdline, []byte("dockerd\x00")) {
			// blocking callback, before we let dockerd continue
			isDocker = true
			accessCb()
		}

		// always reply with allow
		resp := unix.FanotifyResponse{
			Fd:       event.Fd,
			Response: unix.FAN_ALLOW,
		}
		_, err = fanFile.Write(unsafe.Slice((*byte)(unsafe.Pointer(&resp)), unsafe.Sizeof(resp)))
		if err != nil {
			return isDocker, fmt.Errorf("fanotify write: %w", err)
		}
	}

	return isDocker, nil
}

func waitForAccess(fanFile *os.File, accessCb func()) error {
	// as long as all fd usage is pfd-wrapped, we can use Close() to cancel the nonblocking read(), and double close (by defer Close() in handleWormhole) is ok
	defer fanFile.Close()

	var buf [1024]byte
	for {
		n, err := fanFile.Read(buf[:])
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				// race with session end
				return nil
			} else {
				return fmt.Errorf("fanotify read: %w", err)
			}
		}

		// parse events
		p := buf[:n]
		for len(p) > 0 {
			event := (*unix.FanotifyEventMetadata)(unsafe.Pointer(&p[0]))

			if event.Vers != unix.FANOTIFY_METADATA_VERSION {
				return fmt.Errorf("unsupported fanotify version: %d", event.Vers)
			}

			isDocker, err := handleFanotify(fanFile, event, accessCb)
			if err != nil {
				return err
			}
			if isDocker {
				// we've done our job: dockerd has started this container
				return nil
			}

			p = p[event.Event_len:]
		}
	}
}

type wormholeSessionParams struct {
	runOnHost bool
	resp      *agent.StartWormholeResponseClient
}

func (sv *SshServer) prepareWormhole(container *Container, wormholeTarget string) (_params *wormholeSessionParams, retErr error) {
	if conf.Debug() && wormholeTarget == sshtypes.WormholeIDHost {
		// debug only: wormhole for VM host (ovm)
		rootfsFd, err := unix.Open("/proc/thread-self/root", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		rootfsFile := os.NewFile(uintptr(rootfsFd), "rootfs")

		return &wormholeSessionParams{
			runOnHost: true,
			resp: &agent.StartWormholeResponseClient{
				StartWormholeResponse: agent.StartWormholeResponse{
					WorkingDir: "/",
				},
				RootfsFile: rootfsFile,
			},
		}, nil
	}

	// standard path: for docker containers
	var err error
	resp, err := UseAgentRet(container, func(a *agent.Client) (*agent.StartWormholeResponseClient, error) {
		return a.DockerStartWormhole(agent.StartWormholeArgs{
			Target: wormholeTarget,
		})
	})
	if err != nil {
		return nil, err
	}

	return &wormholeSessionParams{
		runOnHost: false,
		resp:      resp,
	}, nil
}

func (sv *SshServer) startWormholeProcess(s ssh.Session, cmd *agent.AgentCommand, container *Container, params *wormholeSessionParams, shellCmd string, meta *sshtypes.SshMeta) (_exitCodePipeRead *os.File, retErr error) {
	isNix, err := isNixContainer(params.resp.RootfsFile)
	if err != nil {
		return nil, err
	}
	if isNix && meta.WormholeFallback {
		return nil, &ExitError{status: sshenv.ExitCodeNixDebugUnsupported}
	}

	err = sv.m.wormhole.OnSessionStart()
	if err != nil {
		return nil, err
	}

	wormholeMountFd, err := unix.OpenTree(unix.AT_FDCWD, mounts.WormholeUnifiedNix, unix.OPEN_TREE_CLOEXEC|unix.OPEN_TREE_CLONE|unix.AT_RECURSIVE)
	if err != nil {
		return nil, err
	}
	wormholeMountFile := os.NewFile(uintptr(wormholeMountFd), "wormhole mount")
	defer wormholeMountFile.Close()

	workDir := params.resp.WorkingDir
	if meta.Pwd != "" {
		workDir = meta.Pwd
	}

	exitCodePipeRead, exitCodePipeWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer exitCodePipeWrite.Close()

	logPipeRead, logPipeWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer logPipeWrite.Close()

	go io.Copy(os.Stderr, logPipeRead)

	config := &sshtypes.WormholeConfig{
		InitPid: params.resp.InitPid,

		// dup starting at fd 3 in child
		WormholeMountTreeFd: 3,
		ExitCodePipeWriteFd: 4,
		LogFd:               5,
		DrmToken:            sv.m.drm.lastResult.EntitlementToken,

		// instead of launching wormhole-attach process with the container's env, we pass it separately because there are several env priorities:
		// 1. start with container env (* from scon)
		// 2. override with pid 1 env
		// 3. override with required wormhole env
		// 4. override with TERM, etc. (* from scon)
		// #1 and #4 are both from scon, so must be separate
		ContainerWorkdir: workDir,
		ContainerEnv:     params.resp.Env,

		EntryShellCmd: shellCmd,
	}

	cmd.User = ""
	cmd.DoLogin = false
	cmd.ReplaceShell = false
	cmd.ExtraFiles = []*os.File{wormholeMountFile, exitCodePipeWrite, logPipeWrite}
	if params.resp.SwitchRoot {
		cmd.ExtraFiles = append(cmd.ExtraFiles, params.resp.RootfsFile)
		config.RootfsFd = 6 // starting at 3
	}

	configBytes, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	cmd.CombinedArgs = []string{mounts.WormholeAttach, string(configBytes)}
	// for debugging (not passed through to payload)
	cmd.Env.SetPair("RUST_BACKTRACE=full")

	if params.runOnHost {
		err = cmd.StartOnHost()
	} else {
		err = container.UseAgent(func(a *agent.Client) error {
			return cmd.Start(a)
		})
	}
	if err != nil {
		return nil, err
	}

	return exitCodePipeRead, nil
}

func (sv *SshServer) waitForWormholeProcess(s ssh.Session, isPty bool, wormholeTarget string, cmd *agent.AgentCommand, initPidfd *agent.PidfdProcess, exitCodePipeRead *os.File, fanotifyFile *os.File) error {
	// process spawned. start monitoring fanotify
	var processWg sync.WaitGroup
	processWg.Add(1)
	if fanotifyFile != nil {
		go func() {
			logrus.Debug("waiting for container start access")
			err := waitForAccess(fanotifyFile, func() {
				logrus.Info("container start detected, killing wormhole session")
				_, _ = io.WriteString(s.Stderr(), ptyWarning(isPty, fmt.Sprintf("\n\nContainer '%s' is starting or being deleted.\nEnding Debug Shell session.\n", wormholeTarget)))

				// killing pid 1 kills everything in the container
				// kernel waits for other processes in the pidns to exit before reaping the pid 1
				err := initPidfd.Kill()
				if err != nil {
					// be lax about errors: we should always reply to the permission request
					logrus.WithError(err).Error("failed to kill payload")
				}

				// synchronously wait for pid 1 exit
				// this ensures that all in-container processes exit
				err = initPidfd.Wait()
				if err != nil {
					logrus.WithError(err).Error("failed to wait for payload exit")
				}

				// ... and also wait for monitor to exit
				// this makes sure that *all* mount refs have been released
				processWg.Wait()

				logrus.Debug("container start granted")
			})
			if err != nil {
				logrus.WithError(err).Error("container start access wait failed")
			}
		}()
	}

	// kill payload if session is closed
	go func() {
		<-s.Context().Done()
		_ = cmd.Process.Signal(unix.SIGPWR)
	}()

	// forward signals using custom map
	sigChan := make(chan ssh.Signal, 1)
	defer close(sigChan)
	defer s.Signals(nil)
	s.Signals(sigChan)
	go func() {
		for sshSig := range sigChan {
			sig, ok := sshWormholeSigMap[sshSig]
			if !ok {
				continue
			}

			err := cmd.Process.Signal(sig)
			if err != nil {
				logrus.WithError(err).Error("ssh signal forward failed in wormhole")
			}
		}
	}()

	go func() {
		_, err := cmd.Process.WaitStatus()
		if err != nil {
			logrus.WithError(err).Error("couldn't wait on wormhole")
			return
		}

		// signal monitor exit
		processWg.Done()

		err = sv.m.wormhole.OnSessionEnd()
		if err != nil {
			logrus.WithError(err).Error("end host wormhole session failed")
		}
	}()

	statusBytes := make([]byte, 1) // exit codes only range from 0-255 so it should be able to fit into a single byte
	n, err := exitCodePipeRead.Read(statusBytes)
	if err != nil {
		return fmt.Errorf("read exit code: %w", err)
	}
	if n < 1 {
		return errors.New("read exit code: short read")
	}

	status := int(statusBytes[0])
	if status != 0 {
		return &ExitError{status: status}
	}

	return nil
}

func (sv *SshServer) handleWormhole(s ssh.Session, cmd *agent.AgentCommand, container *Container, wormholeTarget string, shellCmd string, meta *sshtypes.SshMeta, isPty bool) (bool, error) {
	params, err := sv.prepareWormhole(container, wormholeTarget)
	if err != nil {
		return true /*printErr*/, err
	}
	if params.resp.FanotifyFile != nil {
		defer params.resp.FanotifyFile.Close()
	}
	defer func() {
		resp, err := UseAgentRet(container, func(a *agent.Client) (*agent.EndWormholeResponse, error) {
			return a.DockerEndWormhole(agent.EndWormholeArgs{
				State: params.resp.State,
			})
		})
		if err != nil {
			logrus.WithError(err).Error("end wormhole session failed")
			return
		}

		if resp.HasDiff {
			if params.resp.WarnImageWrite {
				_, _ = io.WriteString(s.Stderr(), ptyWarning(isPty, wormholeImageDiffWarning))
			}
			if params.resp.WarnContainerWrite {
				_, _ = io.WriteString(s.Stderr(), ptyWarning(isPty, wormholeContainerDiffWarning))
			}
		}
	}()

	initPidfd := agent.WrapPidfdFile(params.resp.InitPidfdFile)
	defer initPidfd.Close()

	if params.resp.WarnImageWrite {
		_, _ = io.WriteString(s.Stderr(), ptyWarning(isPty, wormholeImageWriteWarning))
	}
	if params.resp.WarnContainerWrite {
		_, _ = io.WriteString(s.Stderr(), ptyWarning(isPty, wormholeContainerWriteWarning))
	}

	exitCodePipeRead, err := sv.startWormholeProcess(s, cmd, container, params, shellCmd, meta)
	params.resp.RootfsFile.Close()
	if err != nil {
		return true /*printErr*/, err
	}

	// no printing errors to terminal once process has started

	err = sv.waitForWormholeProcess(s, isPty, wormholeTarget, cmd, initPidfd, exitCodePipeRead, params.resp.FanotifyFile)
	return false /*printErr*/, err
}
