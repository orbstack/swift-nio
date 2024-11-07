package domainproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/florianl/go-nfqueue"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/mdlayher/netlink"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

func (d *DomainTLSProxy) startQueue() error {
	config := nfqueue.Config{
		NfQueue:      netconf.QueueDomainproxyPending,
		MaxPacketLen: 65536,
		MaxQueueLen:  512,
		Copymode:     nfqueue.NfQnlCopyPacket,
		Logger:       logrus.StandardLogger(),
	}

	queue, err := nfqueue.Open(&config)
	if err != nil {
		return err
	}

	err = queue.SetOption(netlink.NoENOBUFS, true)
	if err != nil {
		return err
	}

	go func() {
		defer queue.Close()

		ctx := context.Background()
		err = queue.RegisterWithErrorFunc(ctx, func(a nfqueue.Attribute) int {
			hasUpstream, err := d.handleNfqueuePacket(a)
			if err != nil {
				logrus.WithError(err).Error("failed to handle nfqueue packet")
			}

			if hasUpstream {
				// there's an upstream, so accept the connection. (accept = continue nftables)
				queue.SetVerdict(*a.PacketID, nfqueue.NfAccept)
			} else {
				// no upstream, so reject the connection.
				// "reject" isn't a verdict, so mark the packet and repeat nftables. our nftables rule will reject it
				var mark uint32
				if a.Mark != nil {
					mark = *a.Mark
				}
				queue.SetVerdictWithMark(*a.PacketID, nfqueue.NfRepeat, int(d.cb.NfqueueMarkReject(mark)))
			}

			// 0 = continue, else = stop read loop
			return 0
		}, func(e error) int {
			logrus.WithError(e).Error("nfqueue error")
			// stop read loop
			return 1
		})
		if err != nil {
			logrus.WithError(err).Error("failed to register with nfqueue")
		}

		// wait forever
		// nothing closes this
		<-ctx.Done()
	}()

	return nil
}

func (d *DomainTLSProxy) handleNfqueuePacket(a nfqueue.Attribute) (bool, error) {
	// first 4 bits = IP version
	payload := *a.Payload
	if len(payload) < 1 {
		return false, errors.New("payload too short")
	}

	ipVersion := payload[0] >> 4
	var decoder gopacket.Decoder
	if ipVersion == 4 {
		decoder = layers.LayerTypeIPv4
	} else if ipVersion == 6 {
		decoder = layers.LayerTypeIPv6
	} else {
		return false, fmt.Errorf("unsupported ip version: %d", ipVersion)
	}

	packet := gopacket.NewPacket(*a.Payload, decoder, gopacket.Lazy)
	var srcIP netip.Addr
	var dstIP netip.Addr
	ipOk1 := false
	ipOk2 := false
	if ip4, ok := packet.NetworkLayer().(*layers.IPv4); ok {
		srcIP, ipOk1 = netip.AddrFromSlice(ip4.SrcIP)
		dstIP, ipOk2 = netip.AddrFromSlice(ip4.DstIP)
	} else if ip6, ok := packet.NetworkLayer().(*layers.IPv6); ok {
		srcIP, ipOk1 = netip.AddrFromSlice(ip6.SrcIP)
		dstIP, ipOk2 = netip.AddrFromSlice(ip6.DstIP)
	} else {
		return false, errors.New("unsupported ip version")
	}
	if !ipOk1 || !ipOk2 {
		return false, errors.New("failed to get ip")
	}

	logrus.WithField("dst_ip", dstIP).Debug("nfqueue packet")

	upstream, err := d.cb.GetUpstreamByAddr(dstIP)
	if err != nil {
		return false, err
	}

	mark := d.cb.GetMark(upstream)
	// var dialer *net.Dialer
	srcIPSlice := srcIP.AsSlice()
	var dialer *net.Dialer
	if upstream.IP.Equal(srcIPSlice) {
		dialer = &net.Dialer{}
	} else {
		dialer = dialerForTransparentBind(srcIPSlice, mark)
	}
	// split timeout between ports
	dialer.Timeout = upstreamDialTimeout / time.Duration(len(upstreamProbePorts))

	// pessimistically keep probing on every SYN until upstream is ready
	if !testUpstreamPorts(dialer, upstream) {
		return false, nil
	}

	// remove from pending map
	mapName := "domainproxy4_pending"
	if dstIP.Is6() {
		mapName = "domainproxy6_pending"
	}
	// may fail in case of race with a concurrent probe
	_ = nft.Run("delete", "element", "inet", d.cb.NftableName(), mapName, fmt.Sprintf("{ %v }", dstIP))

	return true, nil
}

func testUpstreamPorts(dialer *net.Dialer, upstream domainproxytypes.Upstream) bool {
	for _, port := range upstreamProbePorts {
		conn, err := dialer.DialContext(context.Background(), "tcp", net.JoinHostPort(upstream.IP.String(), strconv.Itoa(port)))
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}
