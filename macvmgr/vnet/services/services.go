package services

import (
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	dnssrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/dns"
	hcsrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/hcontrol"
	sshsrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh"
	ntpsrv "github.com/kdrag0n/macvirt/macvmgr/vnet/services/ntp"
	"github.com/sirupsen/logrus"
)

const (
	runDNS      = true
	runNTP      = true
	runHcontrol = true
	runHostSSH  = true
	runSFTP     = false // Android
)

var (
	staticDnsHosts = map[string]dnssrv.StaticHost{
		"host":              {IP4: vnet.HostNatIP4, IP6: vnet.HostNatIP6},
		"host.internal":     {IP4: vnet.HostNatIP4, IP6: vnet.HostNatIP6},
		"services":          {IP4: vnet.ServicesIP4},
		"services.internal": {IP4: vnet.ServicesIP4},
		"gateway":           {IP4: vnet.GatewayIP4, IP6: vnet.GatewayIP6},
		"gateway.internal":  {IP4: vnet.GatewayIP4, IP6: vnet.GatewayIP6},
	}
)

func StartNetServices(n *vnet.Network) {
	addr := netutil.ParseTcpipAddress(vnet.ServicesIP4)

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
		err := sshsrv.ListenHostSSH(n.Stack, addr)
		if err != nil {
			logrus.Error("Failed to start SSH server", err)
		}
	}

	// Host control (8300): HTTP API
	if runHcontrol {
		_, err := hcsrv.ListenHcontrol(n.Stack, addr)
		if err != nil {
			logrus.Error("Failed to start host control server", err)
		}
	}

	// SFTP (22323): Android file sharing
	/*
		if runSFTP {
			err := sftpsrv.ListenSFTP(n.Stack, addr)
			if err != nil {
				logrus.Error("Failed to start SFTP server", err)
			}
		}
	*/
}
