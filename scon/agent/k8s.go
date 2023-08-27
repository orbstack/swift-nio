package agent

import (
	"net"
	"net/netip"
	"os"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

type K8sAgent struct {
	docker *DockerAgent
}

func (a *K8sAgent) PostStart() error {
	go func() {
		err := a.WaitAndSendKubeConfig()
		if err != nil {
			logrus.WithError(err).Error("failed to send kubeconfig")
		}
	}()

	// create .orbholder dummy interface for k8s bridge arp proxy
	// services CIDR (.128/25) does not actually exist as addrs or routes; it's all done in iptables PREROUTING
	// so this is needed for linux to respond to arp
	la := netlink.NewLinkAttrs()
	la.Name = ".orbholder"
	dummy := &netlink.Dummy{LinkAttrs: la}
	err := netlink.LinkAdd(dummy)
	if err != nil {
		return err
	}

	// add addr
	// interface does not need to be brought up
	_, servicesNet4, err := net.ParseCIDR(netconf.K8sServiceCIDR4)
	if err != nil {
		return err
	}
	err = netlink.AddrAdd(dummy, &netlink.Addr{
		IPNet: servicesNet4,
	})
	if err != nil {
		return err
	}
	_, servicesNet6, err := net.ParseCIDR(netconf.K8sServiceCIDR6)
	if err != nil {
		return err
	}
	err = netlink.AddrAdd(dummy, &netlink.Addr{
		IPNet: servicesNet6,
	})
	if err != nil {
		return err
	}

	// best-effort: create k8s bridge using vlan infra
	logrus.Debug("k8s: creating k8s bridge")
	bridgeConfig := sgtypes.DockerBridgeConfig{
		IP4Subnet: netip.MustParsePrefix(netconf.K8sMergedCIDR4),
		IP6Subnet: netip.MustParsePrefix(netconf.K8sMergedCIDR6),
		// no interface
	}
	err = a.docker.scon.DockerAddBridge(bridgeConfig)
	if err != nil {
		logrus.WithError(err).Error("failed to create k8s bridge")
	}

	return nil
}

func (a *K8sAgent) WaitAndSendKubeConfig() error {
	// wait for kubeconfig
	// parent /run always exists because we mount it in docker machine
	logrus.Debug("k8s: waiting for kubeconfig")
	// wait for this symlink. it's created after /run/kubeconfig.yml is fully written, so it's race-free
	err := util.WaitForPathExist("/etc/rancher/k3s/k3s.yaml", false /*requireWriteClose*/)
	if err != nil {
		return err
	}

	kubeConfigData, err := os.ReadFile("/run/kubeconfig.yml")
	if err != nil {
		return err
	}

	// send to host
	logrus.Debug("k8s: sending kubeconfig to host")
	err = a.docker.host.OnK8sConfigReady(string(kubeConfigData))
	if err != nil {
		return err
	}

	return nil
}
