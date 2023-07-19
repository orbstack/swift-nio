package services

import (
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/netutil"
	dnssrv "github.com/orbstack/macvirt/vmgr/vnet/services/dns"
	hcsrv "github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol"
	sshsrv "github.com/orbstack/macvirt/vmgr/vnet/services/hostssh"
	ntpsrv "github.com/orbstack/macvirt/vmgr/vnet/services/ntp"
	"github.com/orbstack/macvirt/vmgr/vnet/services/sshagent"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

var (
	staticDnsHosts = map[string]dnssrv.StaticHost{
		"vm.internal":             {IP4: netconf.GuestIP4, IP6: netconf.GuestIP6},
		"vm.orb.internal":         {IP4: netconf.GuestIP4, IP6: netconf.GuestIP6},
		"host.internal":           {IP4: netconf.HostNatIP4, IP6: netconf.HostNatIP6},
		"host.orb.internal":       {IP4: netconf.HostNatIP4, IP6: netconf.HostNatIP6},
		"host.docker.internal":    {IP4: netconf.HostNatIP4, IP6: netconf.HostNatIP6},
		"host.lima.internal":      {IP4: netconf.HostNatIP4, IP6: netconf.HostNatIP6},
		"docker.internal":         {IP4: netconf.SconDockerIP4, IP6: netconf.SconDockerIP6},
		"docker.orb.internal":     {IP4: netconf.SconDockerIP4, IP6: netconf.SconDockerIP6},
		"services.internal":       {IP4: netconf.ServicesIP4},
		"services.orb.internal":   {IP4: netconf.ServicesIP4},
		"gateway.internal":        {IP4: netconf.GatewayIP4, IP6: netconf.GatewayIP6},
		"gateway.orb.internal":    {IP4: netconf.GatewayIP4, IP6: netconf.GatewayIP6},
		"gateway.docker.internal": {IP4: netconf.GatewayIP4, IP6: netconf.GatewayIP6},

		// compat with old docker
		"docker.for.mac.localhost": {IP4: netconf.HostNatIP4, IP6: netconf.HostNatIP6},
	}

	reverseDnsHosts = map[string]dnssrv.ReverseHost{
		netconf.GuestIP4:    {Name: "vm.internal"},
		netconf.GuestIP6:    {Name: "vm.internal"},
		netconf.HostNatIP4:  {Name: "host.internal"},
		netconf.HostNatIP6:  {Name: "host.internal"},
		netconf.ServicesIP4: {Name: "services.internal"},
		netconf.GatewayIP4:  {Name: "gateway.internal"},
		netconf.GatewayIP6:  {Name: "gateway.internal"},
	}
)

func StartNetServices(n *vnet.Network) *hcsrv.HcontrolServer {
	addr := netutil.ParseTcpipAddress(netconf.ServicesIP4)
	secureAddr := netutil.ParseTcpipAddress(netconf.SecureSvcIP4)

	// DNS (53): using system resolver
	dnsServer, err := dnssrv.ListenDNS(n.Stack, addr, staticDnsHosts, reverseDnsHosts)
	if err != nil {
		logrus.Error("Failed to start DNS server: ", err)
	}
	n.Proxy.DnsServer = dnsServer

	// NTP (123): using system time
	err = ntpsrv.ListenNTP(n.Stack, addr)
	if err != nil {
		logrus.Error("Failed to start NTP server: ", err)
	}

	// SSH (22): for commands
	err = sshsrv.ListenHostSSH(n.Stack, secureAddr)
	if err != nil {
		logrus.Error("Failed to start SSH server: ", err)
	}

	// Host control (8300): HTTP API
	hcServer, err := hcsrv.ListenHcontrol(n, secureAddr)
	if err != nil {
		logrus.Error("Failed to start host control server: ", err)
	}

	// SSH agent (23): for SSH keys
	err = sshagent.ListenHostSSHAgent(n.Stack, secureAddr)
	if err != nil {
		logrus.Error("Failed to start SSH agent server: ", err)
	}

	// Docker remote ctx (2376)
	// TODO move to secure
	err = ListenHostDockerRemoteCtx(n.Stack, addr)
	if err != nil {
		logrus.Error("Failed to start Docker remote ctx server: ", err)
	}

	return hcServer
}

func ListenHostDockerRemoteCtx(stack *stack.Stack, address tcpip.Address) error {
	_, err := tcpfwd.ListenUnixNATForward(stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.ServiceDockerRemoteCtx,
	}, conf.DockerRemoteCtxSocket())
	if err != nil {
		return err
	}

	return nil
}
