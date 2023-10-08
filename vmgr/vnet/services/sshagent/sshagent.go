package sshagent

import (
	"os"
	"path"
	"strings"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/util/sshconfig"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol/htypes"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd"
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
	configSock, err := sshconfig.ReadKeyForHost(appid.ShortAppName, "IdentityAgent")
	if err == nil && configSock != "" {
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
	} else if err != nil {
		logrus.WithError(err).Warn("failed to read ssh config")
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

	_, err := tcpfwd.ListenUnixNATForward(stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.SecureSvcHostSSHAgent,
	}, agentSock, true)
	if err != nil {
		return err
	}

	return nil
}
