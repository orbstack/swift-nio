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
			err := d.handleNfqueuePacket(a.Attribute)
			if err != nil {
				logrus.WithError(err).Error("failed to handle nfqueue packet")
			}

			var mark uint32
			if a.Mark != nil {
				mark = *a.Mark
			}
			// always skip. presence in the probed sets determines whether the packet is accepted or rejected
			mark = d.cb.NfqueueMarkSkip(mark)

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

func (d *DomainTLSProxy) handleNfqueuePacket(a nfqueue.Attribute) error {
	// first 4 bits = IP version
	payload := *a.Payload
	if len(payload) < 1 {
		return errors.New("payload too short")
	}

	ipVersion := payload[0] >> 4
	var decoder gopacket.Decoder
	if ipVersion == 4 {
		decoder = layers.LayerTypeIPv4
	} else if ipVersion == 6 {
		decoder = layers.LayerTypeIPv6
	} else {
		return fmt.Errorf("unsupported ip version: %d", ipVersion)
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
		return errors.New("unsupported ip version")
	}
	if !ipOk1 || !ipOk2 {
		return errors.New("failed to get ip")
	}

	logrus.WithField("dst_ip", dstIP).Debug("nfqueue packet")

	err := d.probeHost(dstIP, srcIP)
	if err != nil {
		return err
	}

	return nil
}

func (d *DomainTLSProxy) probeHost(addr netip.Addr, downstreamIP netip.Addr) error {
	upstream, err := d.cb.GetUpstreamByAddr(addr)
	if err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"upstream": upstream.IP.String(),
		"src_ip":   downstreamIP.String(),
	}).Debug("upstream")

	mark := d.cb.GetMark(upstream)
	srcIPSlice := downstreamIP.AsSlice()
	var dialer *net.Dialer
	if upstream.IP.Equal(srcIPSlice) {
		dialer = &net.Dialer{}
	} else {
		dialer = dialerForTransparentBind(srcIPSlice, mark)
	}

	// pessimistically keep probing on every SYN until upstream is ready
	httpPort, httpsPort, err := d.probeUpstream(dialer, upstream)
	if err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{
		"addr":          addr.String(),
		"upstream.IP":   upstream.IP.String(),
		"upstream.Host": upstream.Host,
		"http_port":     httpPort,
		"https_port":    httpsPort,
	}).Debug("domaintlsproxy: probe sucessful")

	// lock to update probedHosts map and to ensure no concurrent nft operations
	d.probeMu.Lock()
	defer d.probeMu.Unlock()

	if httpPort != 0 || httpsPort != 0 {
		probed := probedHost{
			HTTPPort:  httpPort,
			HTTPSPort: httpsPort,
		}
		d.probedHosts[addr] = probed
	}

	// update probed set
	nftPrefix := "domainproxy4_probed"
	if addr.Is6() {
		nftPrefix = "domainproxy6_probed"
	}

	// https can use either http or https port
	if httpPort != 0 || httpsPort != 0 {
		// may fail in case of race with a concurrent probe
		err = nft.WithTable(nft.FamilyInet, d.cb.NftableName(), func(conn *nftables.Conn, table *nftables.Table) error {
			return nft.SetAddByName(conn, table, nftPrefix+"_tls", nft.IPAddr(addr))
		})
		if err != nil && !errors.Is(err, unix.EEXIST) {
			logrus.WithError(err).Error("failed to add to domainproxy set")
		}
	} else {
		// remove from probed set
		err = nft.WithTable(nft.FamilyInet, d.cb.NftableName(), func(conn *nftables.Conn, table *nftables.Table) error {
			return nft.SetDeleteByName(conn, table, nftPrefix+"_tls", nft.IPAddr(addr))
		})
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove from domainproxy set")
		}
	}

	// http can obviously only use http port
	if httpPort != 0 {
		// set http upstream
		err = nft.WithTable(nft.FamilyInet, d.cb.NftableName(), func(conn *nftables.Conn, table *nftables.Table) error {
			return nft.MapAddByName(conn, table, nftPrefix+"_http_upstreams", nft.IPAddr(addr), nft.Concat(nft.IP(upstream.IP), nft.InetService(httpPort)))
		})
		if err != nil && !errors.Is(err, unix.EEXIST) {
			logrus.WithError(err).Error("failed to set http upstream")
		}
	} else {
		// remove http upstream
		err = nft.WithTable(nft.FamilyInet, d.cb.NftableName(), func(conn *nftables.Conn, table *nftables.Table) error {
			return nft.MapDeleteByName(conn, table, nftPrefix+"_http_upstreams", nft.IPAddr(addr))
		})
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove http upstream")
		}
	}

	return nil
}

func (d *DomainTLSProxy) probeUpstream(dialer *net.Dialer, upstream domainproxytypes.Upstream) (uint16, uint16, error) {
	ports, err := d.cb.GetHostOpenPorts(upstream.Host)
	if err != nil {
		return 0, 0, err
	}

	logrus.WithFields(logrus.Fields{
		"ports":    ports,
		"upstream": upstream,
	}).Debug("probing host")

	addr, ok := netip.AddrFromSlice(upstream.IP)
	if !ok {
		return 0, 0, fmt.Errorf("failed to get addr from slice: %s", upstream.IP)
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

		probe = portprober.NewHostProbe(ctx, portprober.HostProbeOptions{
			ErrFunc: func(err error) {
				logrus.WithError(err).Error("failed to probe host")
			},
			Dialer: dialer,

			Host:       upstream.IP.String(),
			ServerName: serverName,

			GraceTime: probeGraceTime,
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

	var httpPort uint16
	var httpsPort uint16

	if len(probeResult.HTTPPorts) > 0 {
		httpPorts := make([]uint16, 0, len(probeResult.HTTPPorts))
		for p := range probeResult.HTTPPorts {
			httpPorts = append(httpPorts, p)
		}
		httpPort = slices.Min(httpPorts)
	}

	if len(probeResult.HTTPSPorts) > 0 {
		httpsPorts := make([]uint16, 0, len(probeResult.HTTPSPorts))
		for p := range probeResult.HTTPSPorts {
			httpsPorts = append(httpsPorts, p)
		}
		httpsPort = slices.Min(httpsPorts)
	}

	return httpPort, httpsPort, nil
}
