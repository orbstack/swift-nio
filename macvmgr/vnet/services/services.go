package services

import (
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netconf"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	dnssrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/dns"
	hcsrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/hcontrol"
	sshsrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh"
	ntpsrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/ntp"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/sshagent"
	"github.com/sirupsen/logrus"
)

const (
	runDNS      = true
	runNTP      = true
	runHcontrol = true
	runHostSSH  = true
	runSSHAgent = true
	runSFTP     = false // Android
)

var (
	staticDnsHosts = map[string]dnssrv.StaticHost{
		"vm":                {IP4: netconf.GuestIP4, IP6: netconf.GuestIP6},
		"vm.internal":       {IP4: netconf.GuestIP4, IP6: netconf.GuestIP6},
		"host":              {IP4: netconf.HostNatIP4, IP6: netconf.HostNatIP6},
		"host.internal":     {IP4: netconf.HostNatIP4, IP6: netconf.HostNatIP6},
		"services":          {IP4: netconf.ServicesIP4},
		"services.internal": {IP4: netconf.ServicesIP4},
		"gateway":           {IP4: netconf.GatewayIP4, IP6: netconf.GatewayIP6},
		"gateway.internal":  {IP4: netconf.GatewayIP4, IP6: netconf.GatewayIP6},
	}
)

func StartNetServices(n *vnet.Network) {
	addr := netutil.ParseTcpipAddress(netconf.ServicesIP4)
	secureAddr := netutil.ParseTcpipAddress(netconf.SecureSvcIP4)

	// DNS (53): using system resolver
	if runDNS {
		err := dnssrv.ListenDNS(n.Stack, addr, staticDnsHosts)
		if err != nil {
			logrus.Error("Failed to start DNS server", err)
		}
	}

	// NTP (123): using system time
	if runNTP {
		err := ntpsrv.ListenNTP(n.Stack, addr)
		if err != nil {
			logrus.Error("Failed to start NTP server", err)
		}
	}

	// SSH (22): for commands
	if runHostSSH {
		err := sshsrv.ListenHostSSH(n.Stack, secureAddr)
		if err != nil {
			logrus.Error("Failed to start SSH server", err)
		}
	}

	// Host control (8300): HTTP API
	if runHcontrol {
		_, err := hcsrv.ListenHcontrol(n, secureAddr)
		if err != nil {
			logrus.Error("Failed to start host control server", err)
		}
	}

	// SSH agent (23): for SSH keys
	if runSSHAgent {
		err := sshagent.ListenHostSSHAgent(n.Stack, secureAddr)
		if err != nil {
			logrus.Error("Failed to start SSH agent server", err)
		}
	}

	// SFTP (22323): Android file sharing
	/*
		if runSFTP {
			err := sftpsrv.ListenSFTP(n.Stack, secureAddr)
			if err != nil {
				logrus.Error("Failed to start SFTP server", err)
			}
		}
	*/
}
