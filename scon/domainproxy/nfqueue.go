package domainproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/florianl/go-nfqueue"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/nftables"
	"github.com/mdlayher/netlink"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/portprober"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func (d *DomainTLSProxy) startQueue(queueNum uint16) error {
	ctx, cancel := context.WithCancel(context.Background())

	return util.RunNfqueue(ctx, util.NfqueueConfig{
		Config: &nfqueue.Config{
			NfQueue:      queueNum,
			MaxPacketLen: 65536,
			MaxQueueLen:  1024,
			Copymode:     nfqueue.NfQnlCopyPacket,
			Flags:        0,
			Logger:       logrus.StandardLogger(),
		},
		Options: map[netlink.ConnOption]bool{
			netlink.NoENOBUFS: true,
		},
		AttributeHandler: func(ctx context.Context, queue *nfqueue.Nfqueue, a util.NfqueueLinkedAttribute) {
			hasUpstream, err := d.handleNfqueuePacket(a.Attribute)
			if err != nil {
				logrus.WithError(err).Error("failed to handle nfqueue packet")
			}

			var mark uint32
			if a.Mark != nil {
				mark = *a.Mark
			}

			if hasUpstream {
				// there's an upstream, so accept the connection. (accept = continue nftables)
				// with GSO, we must always pass the payload back even if unmodified, or the GSO type breaks
				logrus.Debug("nfqueue: accept")
				mark = d.cb.NfqueueMarkSkip(mark)
			} else {
				// no upstream, so reject the connection.
				// "reject" isn't a verdict, so mark the packet and repeat nftables. our nftables rule will reject it
				logrus.Debug("nfqueue: reject")
				mark = d.cb.NfqueueMarkReject(mark)
			}
			err = a.SetVerdictModPacketWithMark(nfqueue.NfRepeat, *a.Payload, mark)
			if err != nil {
				logrus.WithError(err).Error("failed to set verdict")
			}

		},
		ErrorHandler: func(ctx context.Context, e error) {
			logrus.WithError(e).Error("nfqueue error")
			cancel()
		},
	})
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
	srcIPSlice := srcIP.AsSlice()
	var dialer *net.Dialer
	if upstream.IP.Equal(srcIPSlice) {
		dialer = &net.Dialer{}
	} else {
		dialer = dialerForTransparentBind(srcIPSlice, mark)
	}

	// pessimistically keep probing on every SYN until upstream is ready
	upstreamPort, err := d.probeHost(dialer, upstream)
	if err != nil {
		return false, err
	}
	if upstreamPort == (serverPort{}) {
		logrus.Debug("domaintlsproxy: probe did not find a port")
		return false, nil
	}

	// add to probed set
	setName := "domainproxy4_probed"
	if dstIP.Is6() {
		setName = "domainproxy6_probed"
	}
	// may fail in case of race with a concurrent probe
	err = nft.WithTable(nft.FamilyInet, d.cb.NftableName(), func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.SetAddByName(conn, table, setName, nft.IPAddr(dstIP))
	})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		logrus.WithError(err).Error("failed to add to domainproxy set")
	}

	return true, nil
}

func (d *DomainTLSProxy) probeHost(dialer *net.Dialer, upstream domainproxytypes.Upstream) (serverPort, error) {
	ports, err := d.cb.GetHostOpenPorts(upstream.Host)
	if err != nil {
		return serverPort{}, err
	}

	addr, ok := netip.AddrFromSlice(upstream.IP)
	if !ok {
		return serverPort{}, fmt.Errorf("failed to get addr from slice: %s", upstream.IP)
	}

	d.probeMu.Lock()
	probe, ok := d.probeTasks[addr]
	if !ok {
		serverName := ""
		if len(upstream.Names) > 0 {
			serverName = upstream.Names[0]
		}

		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()

		probe = portprober.NewHostProbe(portprober.HostProbeOptions{
			ErrFunc: func(err error) {
				logrus.WithError(err).Error("failed to probe host")
			},
			Dialer: dialer,

			Host:       upstream.IP.String(),
			ServerName: serverName,

			GraceTime: probeGraceTime,
			Ctx:       ctx,
		})
		d.probeTasks[addr] = probe
	}
	d.probeMu.Unlock()

	probeResult := probe.Probe(ports)

	d.probeMu.Lock()
	defer d.probeMu.Unlock()

	if p, ok := d.probeTasks[addr]; ok && p == probe {
		delete(d.probeTasks, addr)
	}

	var port serverPort

	if len(probeResult.HTTPSPorts) > 0 {
		httpPorts := make([]uint16, 0, len(probeResult.HTTPSPorts))
		for p := range probeResult.HTTPSPorts {
			httpPorts = append(httpPorts, p)
		}
		port = serverPort{
			// use lowest port
			port:  slices.Min(httpPorts),
			https: true,
		}
	}

	if len(probeResult.HTTPPorts) > 0 {
		httpPorts := make([]uint16, 0, len(probeResult.HTTPPorts))
		for p := range probeResult.HTTPPorts {
			httpPorts = append(httpPorts, p)
		}
		port = serverPort{
			port:  slices.Min(httpPorts),
			https: false,
		}
	}

	if port != (serverPort{}) {
		d.probedHosts[addr] = port
	}

	return port, nil
}
