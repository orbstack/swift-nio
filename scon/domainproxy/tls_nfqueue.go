package domainproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/florianl/go-nfqueue"
	"github.com/google/nftables"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/portprober"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func (d *DomainTLSProxy) startQueue(queueNum uint16) error {
	return util.RunNfqueue(context.TODO(), util.NfqueueConfig{
		QueueNum: queueNum,
		AttributeHandler: func(ctx context.Context, queue *nfqueue.Nfqueue, a util.NfqueueLinkedAttribute) {
			err := d.handleNfqueuePacket(a)
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
	})
}

func (d *DomainTLSProxy) handleNfqueuePacket(a util.NfqueueLinkedAttribute) error {
	srcIP, dstIP, err := a.ParseIPs()
	if err != nil {
		return err
	}

	logrus.WithField("dst_ip", dstIP).Debug("tls nfqueue packet")

	_, err = d.probeHost(dstIP, srcIP)
	if err != nil {
		return err
	}

	return nil
}

func (d *DomainTLSProxy) probeHost(addr netip.Addr, downstreamIP netip.Addr) (probedHost, error) {
	upstream, err := d.cb.GetUpstreamByAddr(addr)
	if err != nil {
		return probedHost{}, err
	}

	logrus.WithFields(logrus.Fields{
		"upstream": upstream.IP.String(),
		"src_ip":   downstreamIP.String(),
	}).Debug("upstream")

	var httpPort uint16
	var httpsPort uint16
	if upstream.HTTPPortOverride == 0 || upstream.HTTPSPortOverride == 0 {
		mark := d.cb.GetMark(upstream)
		srcIPSlice := downstreamIP.AsSlice()
		var dialer *net.Dialer
		if upstream.IP.Equal(srcIPSlice) {
			dialer = &net.Dialer{}
		} else {
			dialer = dialerForTransparentBind(srcIPSlice, mark)
		}

		// pessimistically keep probing on every SYN until upstream is ready
		httpPort, httpsPort, err = d.probeUpstream(dialer, upstream)
		if err != nil {
			return probedHost{}, err
		}
	}

	if upstream.HTTPPortOverride != 0 {
		httpPort = upstream.HTTPPortOverride
	}
	if upstream.HTTPSPortOverride != 0 {
		httpsPort = upstream.HTTPSPortOverride
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

	var probed probedHost
	if httpPort != 0 || httpsPort != 0 {
		probed = probedHost{
			HTTPPort:  httpPort,
			HTTPSPort: httpsPort,
		}
		d.probedHosts[addr] = probed
	} else {
		delete(d.probedHosts, addr)
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

	return probed, nil
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
