package domainproxy

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/florianl/go-nfqueue"
	"github.com/google/nftables"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func (d *DomainSSHProxy) startQueue() error {
	return util.RunNfqueue(context.TODO(), util.NfqueueConfig{
		QueueNum: netconf.QueueDomainproxySshProbe,
		AttributeHandler: func(ctx context.Context, queue *nfqueue.Nfqueue, a util.NfqueueLinkedAttribute) {
			err := d.handleNfqueuePacket(ctx, a)
			if err != nil {
				logrus.WithError(err).Error("failed to handle nfqueue packet")
			}

			// on success, go back to nftables; it'll determine whether to dnat or accept (proxy)
			// on error, drop the packet so client can retry
			verdict := nfqueue.NfRepeat
			if err != nil {
				verdict = nfqueue.NfDrop
			}
			err = a.SetVerdictModPacketWithMark(verdict, *a.Payload, netconf.VmFwmarkNfqueueSkipBit)
			if err != nil {
				logrus.WithError(err).Error("failed to set verdict")
			}
		},
	})
}

func (d *DomainSSHProxy) handleNfqueuePacket(ctx context.Context, a util.NfqueueLinkedAttribute) error {
	srcIP, dstIP, err := a.ParseIPs()
	if err != nil {
		return fmt.Errorf("parse ips: %w", err)
	}

	logrus.WithField("dst_ip", dstIP).Debug("ssh nfqueue packet")

	upstream, err := d.cb.GetUpstreamByAddr(dstIP)
	if err != nil {
		return fmt.Errorf("get upstream: %w", err)
	}

	// race: upstream just changed from machine to docker container
	// go back to nftables; it'll no longer be in the @domainproxy#_docker map so it'll just dnat
	if upstream.Host.Type != domainproxytypes.HostTypeMachine {
		return errors.New("upstream is not a machine")
	}

	// attempt a connection
	hasUpstream := true
	dialer := dialerForTransparentBind(srcIP.AsSlice(), netconf.VmFwmarkTproxyOutboundBit)
	dialer.Timeout = probeGraceTime
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(upstream.IP.String(), "22"))
	if err != nil {
		if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, unix.ETIMEDOUT) {
			// no upstream server
			hasUpstream = false
		} else {
			// error. drop the packet and retry
			return fmt.Errorf("dial: %w", err)
		}
	} else {
		conn.Close()
	}

	// success: add to @domainproxy4_probed_ssh_upstream
	var setName string
	if hasUpstream {
		setName = "domainproxy4_probed_ssh_upstream"
		if dstIP.Is6() {
			setName = "domainproxy6_probed_ssh_upstream"
		}
	} else {
		setName = "domainproxy4_probed_ssh_no_upstream"
		if dstIP.Is6() {
			setName = "domainproxy6_probed_ssh_no_upstream"
		}
	}
	err = nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.SetAddByName(conn, table, setName, nft.IPAddr(dstIP))
	})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("set add: %w", err)
	}

	return nil
}
