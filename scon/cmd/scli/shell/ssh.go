package shell

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/orbstack/macvirt/scon/agent/envutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf/sshenv"
	"github.com/orbstack/macvirt/vmgr/conf/sshpath"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/termios"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

var (
	sshSigMap = map[os.Signal]ssh.Signal{
		unix.SIGABRT: ssh.SIGABRT,
		unix.SIGALRM: ssh.SIGALRM,
		unix.SIGFPE:  ssh.SIGFPE,
		unix.SIGHUP:  ssh.SIGHUP,
		unix.SIGILL:  ssh.SIGILL,
		unix.SIGINT:  ssh.SIGINT,
		unix.SIGPIPE: ssh.SIGPIPE,
		unix.SIGQUIT: ssh.SIGQUIT,
		unix.SIGSEGV: ssh.SIGSEGV,
		unix.SIGTERM: ssh.SIGTERM,
		unix.SIGUSR1: ssh.SIGUSR1,
		unix.SIGUSR2: ssh.SIGUSR2,
	}
)

type CommandOpts struct {
	CombinedArgs     []string
	UseShell         bool
	ExtraEnv         map[string]string
	User             string
	Dir              *string
	ContainerName    string
	WormholeFallback bool
}

func RunSSH(opts CommandOpts) (int, error) {
	if opts.ContainerName == "" {
		opts.ContainerName = "default"
	}
	if opts.User == "" {
		opts.User = "[default]"
	}

	config := &ssh.ClientConfig{
		User:            opts.User + "@" + opts.ContainerName,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	err := vmclient.EnsureSconVM()
	if err != nil {
		return 0, err
	}

	cfg := scli.Conf()
	client, err := ssh.Dial(cfg.SshNet, cfg.SshAddr, config)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return 0, err
	}
	defer session.Close()

	meta := sshtypes.SshMeta{
		RawCommand:       !opts.UseShell && len(opts.CombinedArgs) > 0,
		WormholeFallback: opts.WormholeFallback,
	}

	// individual ptys
	// tell the host which ones should be pipes and which ones should be ptys
	ptyFd := -1
	if term.IsTerminal(fdStdin) {
		meta.PtyStdin = true
		ptyFd = fdStdin
	}
	if term.IsTerminal(fdStdout) {
		meta.PtyStdout = true
		ptyFd = fdStdout
	}
	if term.IsTerminal(fdStderr) {
		meta.PtyStderr = true
		ptyFd = fdStderr
	}
	// need a pty?
	if meta.PtyStdin || meta.PtyStdout || meta.PtyStderr {
		termEnv := os.Getenv("TERM")
		w, h, err := term.GetSize(ptyFd)
		if err != nil {
			return 0, err
		}

		// snapshot the flags
		flags, err := termios.GetTermios(uintptr(ptyFd))
		if err != nil {
			return 0, err
		}
		modes := termios.TermiosToSSH(flags)

		// raw mode if both outputs are ptys
		if meta.PtyStdout && meta.PtyStderr {
			state, err := term.MakeRaw(ptyFd)
			if err != nil {
				return 0, err
			}
			defer term.Restore(ptyFd, state)
		}

		// request pty
		err = session.RequestPty(termEnv, h, w, modes)
		if err != nil {
			return 0, err
		}
	}

	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// forward and translate cwd path
	var cwd string
	if opts.Dir == nil {
		cwd, err = os.Getwd()
		if err == nil {
			// we know target container name, so translate it
			cwd = sshpath.ToLinux(cwd, sshpath.ToLinuxOptions{
				TargetContainer: opts.ContainerName,
			})
		}
	} else {
		// no translation
		cwd = *opts.Dir
	}
	meta.Pwd = cwd

	// forward signals
	fwdSigChan := make(chan os.Signal, 1)
	notifySigs := make([]os.Signal, 0)
	for k := range sshSigMap {
		notifySigs = append(notifySigs, k)
	}
	signal.Notify(fwdSigChan, notifySigs...)

	// handle window change
	winchChan := make(chan os.Signal, 1)
	signal.Notify(winchChan, unix.SIGWINCH)

	// start with only necessary client env
	osEnv := envutil.ToMap(os.Environ())
	clientEnv, err := sshenv.OSToClientEnv(osEnv, sshenv.ToLinux)
	if err != nil {
		return 0, err
	}

	// add extra env
	for k, v := range opts.ExtraEnv {
		clientEnv[k] = v
	}

	// add metadata
	metaBytes, err := json.Marshal(&meta)
	if err != nil {
		return 0, err
	}
	clientEnv[sshtypes.KeyMeta] = string(metaBytes)

	// send all env
	for k, v := range clientEnv {
		err = session.Setenv(k, v)
		if err != nil {
			return 0, err
		}
	}

	if len(opts.CombinedArgs) > 0 {
		if opts.UseShell {
			err = session.Start(strings.Join(opts.CombinedArgs, " "))
			if err != nil {
				return 0, err
			}
		} else {
			// run $0
			combinedArgsBytes, err := json.Marshal(&opts.CombinedArgs)
			if err != nil {
				return 0, err
			}
			err = session.Start(string(combinedArgsBytes))
			if err != nil {
				return 0, err
			}
		}
	} else {
		// no args = shell
		err = session.Shell()
		if err != nil {
			return 0, err
		}
	}

	// wait for done
	doneChan := make(chan error, 1)
	go func() {
		doneChan <- session.Wait()
	}()

	// handle signals, WINCH, and done
	for {
		select {
		case sig := <-fwdSigChan:
			err = session.Signal(sshSigMap[sig])
			if err != nil {
				logrus.WithError(err).Warn("failed to forward signal")
			}
		case <-winchChan:
			w, h, err := term.GetSize(ptyFd)
			if err != nil {
				continue
			}

			err = session.WindowChange(h, w)
			if err != nil {
				logrus.WithError(err).Warn("failed to forward window change")
			}
		case err := <-doneChan:
			if err != nil {
				if exitErr, ok := err.(*ssh.ExitError); ok {
					return exitErr.ExitStatus(), nil
				} else if errors.Is(err, io.EOF) {
					// TODO correct exit status
					return 0, nil
				} else if _, ok := err.(*ssh.ExitMissingError); ok {
					// TODO print message?
					return 1, nil
				} else {
					return 0, err
				}
			}

			return 0, nil
		}
	}
}
