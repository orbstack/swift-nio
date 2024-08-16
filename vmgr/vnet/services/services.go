package services

import (
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/drm"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/netutil"
	dnssrv "github.com/orbstack/macvirt/vmgr/vnet/services/dns"
	hcsrv "github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol"
	sshsrv "github.com/orbstack/macvirt/vmgr/vnet/services/hostssh"
	ntpsrv "github.com/orbstack/macvirt/vmgr/vnet/services/ntp"
	"github.com/orbstack/macvirt/vmgr/vnet/services/readyevents"
	"github.com/orbstack/macvirt/vmgr/vnet/services/sshagent"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

var (
	staticDnsHosts = map[string]dnssrv.StaticHost{
		"vm.orb.internal":   {IP4: netconf.VnetGuestIP4, IP6: netconf.VnetGuestIP6},
		"host.internal":     {IP4: netconf.VnetHostNatIP4, IP6: netconf.VnetHostNatIP6},
		"host.orb.internal": {IP4: netconf.VnetHostNatIP4, IP6: netconf.VnetHostNatIP6},
		// compat: some apps have trouble with v6 (e.g. envoy: https://mail.google.com/mail/u/1/#inbox/FMfcgzGtxKQRRSpLCKZDxmZSBLCjFHXq)
		// docker can't run in v6-only anyway, and v6 rarely helps with anything (even for ::1 because we have fallback dial)
		// keep it for machines, but docker ecosystem is *bad* with v6
		"host.docker.internal":    {IP4: netconf.VnetHostNatIP4},
		"host.lima.internal":      {IP4: netconf.VnetHostNatIP4, IP6: netconf.VnetHostNatIP6},
		"docker.orb.internal":     {IP4: netconf.SconDockerIP4, IP6: netconf.SconDockerIP6},
		"services.orb.internal":   {IP4: netconf.VnetServicesIP4},
		"gateway.orb.internal":    {IP4: netconf.VnetGatewayIP4, IP6: netconf.VnetGatewayIP6},
		"gateway.docker.internal": {IP4: netconf.VnetGatewayIP4, IP6: netconf.VnetGatewayIP6},

		// compat with old docker
		"docker.for.mac.localhost":     {IP4: netconf.VnetHostNatIP4, IP6: netconf.VnetHostNatIP6},
		"docker.for.mac.host.internal": {IP4: netconf.VnetHostNatIP4, IP6: netconf.VnetHostNatIP6},
	}

	// e.g. for ping/traceroute
	reverseDnsHosts = map[string]dnssrv.ReverseHost{
		netconf.VnetGuestIP4:     {Name: "vm.orb.internal"},
		netconf.VnetGuestIP6:     {Name: "vm.orb.internal"},
		netconf.VnetHostNatIP4:   {Name: "host.orb.internal"},
		netconf.VnetHostNatIP6:   {Name: "host.orb.internal"},
		netconf.VnetServicesIP4:  {Name: "services.orb.internal"},
		netconf.VnetSecureSvcIP4: {Name: "services2.orb.internal"},
		netconf.VnetGatewayIP4:   {Name: "gateway.orb.internal"},
		netconf.VnetGatewayIP6:   {Name: "gateway.orb.internal"},
	}
)

type NetServices struct {
	HcServer    *hcsrv.HcontrolServer
	ReadyEvents *readyevents.ReadyEventsService
}

func StartNetServices(n *vnet.Network, drmClient *drm.DrmClient) (*NetServices, error) {
	addr := netutil.ParseTcpipAddress(netconf.VnetServicesIP4)
	secureAddr := netutil.ParseTcpipAddress(netconf.VnetSecureSvcIP4)

	// HID service (8302)
	readyEvents, err := readyevents.ListenReadyEventsService(n.Stack, secureAddr)
	if err != nil {
		logrus.Error("Failed to start HID server: ", err)
		return nil, err
	}

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
	hcServer, err := hcsrv.ListenHcontrol(n, secureAddr, drmClient)
	if err != nil {
		logrus.Error("Failed to start host control server: ", err)
	}

	// SSH agent (23): for SSH keys
	err = sshagent.ListenHostSSHAgent(n.Stack, secureAddr)
	if err != nil {
		logrus.Error("Failed to start SSH agent server: ", err)
	}

	// Docker remote ctx (2376)
	dockerCtxForward, err := ListenHostDockerRemoteCtx(n.Stack, secureAddr)
	if err != nil {
		logrus.Error("Failed to start Docker remote ctx server: ", err)
	}

	n.DockerRemoteCtxForward = dockerCtxForward
	return &NetServices{
		HcServer:    hcServer,
		ReadyEvents: readyEvents,
	}, nil
}

func ListenHostDockerRemoteCtx(stack *stack.Stack, address tcpip.Address) (*tcpfwd.UnixNATForward, error) {
	return tcpfwd.ListenUnixNATForward(stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.SecureSvcDockerRemoteCtx,
	}, "", false) // start in disabled state
}
