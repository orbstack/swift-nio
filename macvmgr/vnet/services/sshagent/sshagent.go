package sshagent

import (
	"os"
	"path"
	"strings"

	"github.com/kevinburke/ssh_config"
	"github.com/orbstack/macvirt/macvmgr/vnet/services/hcontrol/htypes"
	"github.com/orbstack/macvirt/macvmgr/vnet/tcpfwd"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func GetAgentSockets() htypes.SSHAgentSockets {
	var socks htypes.SSHAgentSockets

	// get env
	socks.Env = os.Getenv("SSH_AUTH_SOCK")
	// is it relative?
	if !path.IsAbs(socks.Env) {
		// won't work.
		socks.Env = ""
	}

	// get config
	configSock, err := ssh_config.GetStrict("*", "IdentityAgent")
	if err == nil {
		// the parser sucks... fix quotes and ~/ for 1password
		// TODO parse it ourselves
		configSock = strings.Trim(configSock, "\"")
		if strings.HasPrefix(configSock, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				panic(err)
			}

			configSock = home + configSock[1:]
		}

		socks.SshConfig = configSock
	}

	// prefer IdentityAgent for 1Password agent
	if socks.SshConfig != "" {
		socks.Preferred = socks.SshConfig
	} else {
		socks.Preferred = socks.Env
	}

	return socks
}

func ListenHostSSHAgent(stack *stack.Stack, address tcpip.Address) error {
	agentSock := GetAgentSockets().Preferred
	logrus.WithField("sock", agentSock).Info("forwarding SSH agent")

	_, err := tcpfwd.ListenUnixNATForward(stack, address, agentSock)
	if err != nil {
		return err
	}

	return nil
}
