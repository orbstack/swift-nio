package sshagent

import (
	"os"

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
