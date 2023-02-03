package sshagent

import (
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/tcpfwd"
	"github.com/kevinburke/ssh_config"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func GetAgentSocket() string {
	// prefer IdentityAgent for 1Password agent
	agentSock, err := ssh_config.GetStrict("*", "IdentityAgent")
	if agentSock == "" || err != nil {
		agentSock = os.Getenv("SSH_AUTH_SOCK")
	} else {
		// the parser sucks... fix quotes and ~/ for 1password
		// TODO parse it ourselves
		agentSock = strings.Trim(agentSock, "\"")
		if strings.HasPrefix(agentSock, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				panic(err)
			}

			agentSock = home + agentSock[1:]
		}
	}

	return agentSock
}

func ListenHostSSHAgent(stack *stack.Stack, address tcpip.Address) error {
	agentSock := GetAgentSocket()
	logrus.WithField("sock", agentSock).Info("forwarding SSH agent")

	_, err := tcpfwd.ListenUnixNATForward(stack, address, agentSock)
	if err != nil {
		return err
	}

	return nil
}
