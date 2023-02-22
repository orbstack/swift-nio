package shell

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path"
	"regexp"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/sshtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/termios"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/sys/unix"
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

var (
	pathArgRegexp = regexp.MustCompile(`^([a-zA-Z0-9_\-]+)?=/(.+)$`)

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
	CombinedArgs []string
	UseShell     bool
	ExtraEnv     map[string]string
}

func NfsDataRoot() string {
	user := HostUser()
	if user == "" {
		user = os.Getenv("USER")
	}

	// in scon, hostname = container name
	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	return "/Users/" + user + "/Linux/" + hostname
}

func TranslatePath(p string) string {
	// canonicalize first
	p = path.Clean(p)

	// if path is under mac virtiofs mount, remove the mount prefix
	if p == mounts.Virtiofs {
		return "/"
	} else if strings.HasPrefix(p, mounts.Virtiofs+"/") {
		return strings.TrimPrefix(p, mounts.Virtiofs)
	}

	// nothing to do for linked paths
	for _, linkPrefix := range mounts.LinkedPaths {
		if p == linkPrefix || strings.HasPrefix(p, linkPrefix+"/") {
			return p
		}
	}

	// otherwise, translate to linux
	return NfsDataRoot() + p
}

func TranslatePathRelaxed(p string) string {
	// canonicalize first
	p = path.Clean(p)

	// ONLY translate home
	linuxHome, err := os.UserHomeDir()
	if err != nil {
		// do nothing
		return p
	}

	// ONLY translate home
	if p == linuxHome || strings.HasPrefix(p, linuxHome+"/") {
		return NfsDataRoot() + p
	}

	return p
}

func IsPathArg(arg string) bool {
	// 1. starts with slash
	if strings.HasPrefix(arg, "/") {
		return true
	}

	// 2. -option=/value, --option=/value, or option=/value
	if pathArgRegexp.Match([]byte(arg)) {
		return true
	}

	return false
}

func TranslateArgPaths(args []string) []string {
	for i, arg := range args {
		if IsPathArg(arg) {
			if pathArgRegexp.Match([]byte(arg)) {
				// -option=/value, --option=/value, or option=/value
				matches := pathArgRegexp.FindStringSubmatch(arg)
				args[i] = matches[1] + "=" + TranslatePath(matches[2])
			} else {
				args[i] = TranslatePath(arg)
			}
		}
	}

	return args
}

// only translates /home/<user>
func TranslateArgPathsRelaxed(args []string) []string {
	for i, arg := range args {
		if IsPathArg(arg) {
			if pathArgRegexp.Match([]byte(arg)) {
				// -option=/value, --option=/value, or option=/value
				matches := pathArgRegexp.FindStringSubmatch(arg)
				args[i] = matches[1] + "=" + TranslatePathRelaxed(matches[2])
			} else {
				args[i] = TranslatePathRelaxed(arg)
			}
		}
	}

	return args
}

func ConnectSSH(opts CommandOpts) (int, error) {
	config := &ssh.ClientConfig{
		User: "macctl", // unused, only one user
		// Auth: []ssh.AuthMethod{
		// 	ssh.Password("test"),
		// },
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("unix", mounts.HostSSHSocket, config)
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
		RawCommand: !opts.UseShell && len(opts.CombinedArgs) > 0,
	}

	// individual ptys
	// tell the host which ones should be pipes and which ones should be ptys
	ptyFd := -1
	if terminal.IsTerminal(fdStdin) {
		meta.PtyStdin = true
		ptyFd = fdStdin
	}
	if terminal.IsTerminal(fdStdout) {
		meta.PtyStdout = true
		ptyFd = fdStdout
	}
	if terminal.IsTerminal(fdStderr) {
		meta.PtyStderr = true
		ptyFd = fdStderr
	}
	// need a pty?
	if meta.PtyStdin || meta.PtyStdout || meta.PtyStderr {
		term := os.Getenv("TERM")
		w, h, err := terminal.GetSize(ptyFd)
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
			state, err := terminal.MakeRaw(ptyFd)
			if err != nil {
				return 0, err
			}
			defer terminal.Restore(ptyFd, state)
		}

		// request pty
		err = session.RequestPty(term, h, w, modes)
		if err != nil {
			return 0, err
		}
	}

	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// forward and translate cwd path
	cwd, err := os.Getwd()
	if err == nil {
		meta.Pwd = TranslatePath(cwd)
	}

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

	// send environment (server chooses what to accept)
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		session.Setenv(key, value)
	}

	// extra env
	for k, v := range opts.ExtraEnv {
		session.Setenv(k, v)
	}

	// send metadata
	metaBytes, err := json.Marshal(&meta)
	if err != nil {
		return 0, err
	}
	session.Setenv("__MV_META", string(metaBytes))

	if len(opts.CombinedArgs) > 0 {
		if opts.UseShell {
			err = session.Start(strings.Join(opts.CombinedArgs, " "))
			if err != nil {
				return 0, err
			}
		} else {
			// run $0
			// TODO find and translate paths
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
			w, h, err := terminal.GetSize(ptyFd)
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
				} else {
					return 0, err
				}
			}

			return 0, nil
		}
	}
}
