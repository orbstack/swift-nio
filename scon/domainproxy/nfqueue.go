package domainproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/florianl/go-nfqueue"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/nftables"
	"github.com/mdlayher/netlink"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func (d *DomainTLSProxy) startQueue(queueNum uint16, flags uint32) error {
	config := nfqueue.Config{
		NfQueue:      queueNum,
		MaxPacketLen: 65536,
		MaxQueueLen:  1024,
		Copymode:     nfqueue.NfQnlCopyPacket,
		// declare GSO and partial checksum support to prevent reject from failing on macOS-originated packets (which are GSO + partial csum)
		// we only need GSO flag in ovm, and it breaks the docker bridge, so disable it in docker machine and enable it in ovm
		Flags:  flags,
		Logger: logrus.StandardLogger(),
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
			// handle packets in parallel to minimize happy eyeballs and load testing delays
			go func() {
				hasUpstream, err := d.handleNfqueuePacket(a)
				if err != nil {
					logrus.WithError(err).Error("failed to handle nfqueue packet")
				}

				var mark uint32
				if a.Mark != nil {
					mark = *a.Mark
				}

				if hasUpstream {
					logrus.Debug("soweli | skip")
					// there's an upstream, so accept the connection. (accept = continue nftables)
					// with GSO, we must always pass the payload back even if unmodified, or the GSO type breaks
					mark = d.cb.NfqueueMarkSkip(mark)
				} else {
					logrus.Debug("soweli | reject")
					// no upstream, so reject the connection.
					// "reject" isn't a verdict, so mark the packet and repeat nftables. our nftables rule will reject it
					mark = d.cb.NfqueueMarkReject(mark)
				}
				err = queue.SetVerdictModPacketWithMark(*a.PacketID, nfqueue.NfRepeat, int(mark), *a.Payload)
				if err != nil {
					logrus.WithError(err).Error("failed to set verdict")
				}
			}()

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
		logrus.Debug("soweli | failed to dial")
		return false, nil
	}
	d.probedHostsMu.Lock()
	d.probedHosts[dstIP] = upstreamPort
	d.probedHostsMu.Unlock()

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
	dialer.Timeout = httpsDialTimeout

	upstreamConn, err := dialer.Dial("tcp", net.JoinHostPort(upstream.IP.String(), "443"))
	if err == nil {
		upstreamConn.Close()
		return serverPort{
			port:  443,
			https: true,
		}, nil
	}

	return serverPort{
		port:  80,
		https: false,
	}, nil
}

/*func (d *DomainTLSProxy) probeHost(dialer *net.Dialer, upstream domainproxytypes.Upstream) (serverPort, error) {
	if upstream.ContainerID == "" {
		logrus.Error("unable to probe host: upstream has no container id")
		return serverPort{}, fmt.Errorf("get upstream container id")
	}

	var ports map[uint16]struct{}

	var err error
	if upstream.Docker {
		ports, err = d.cb.GetContainerOpenPorts(upstream.ContainerID)
	} else {
		ports, err = d.cb.GetMachineOpenPorts(upstream.ContainerID)
	}
	if err != nil {
		return serverPort{}, fmt.Errorf("get open ports: %w", err)
	}
	logrus.Debugf("soweli | open ports: %v", ports)

	var httpWg sync.WaitGroup
	var httpsWg sync.WaitGroup

	httpCtx, httpCancel := context.WithCancel(context.Background())
	defer httpCancel()
	httpsCtx, httpsCancel := context.WithCancel(context.Background())
	defer httpsCancel()
	var upstreamPort serverPort

	// we concurrently try to dial every port with tls and http
	// we prefer a tls connection over an http connection, so we give a 500ms timeout for a response to a tls HELO
	// if there's no response in 500ms, we pick http (if we found one) or fail
	for port := range ports {
		httpsWg.Add(1)
		go func() {
			defer httpsWg.Done()

			ctx, cancel := context.WithTimeout(httpsCtx, httpsDialTimeout)
			defer cancel()

			if testPortHTTPS(ctx, dialer, upstream, port) {
				// since we prefer https, we can cancel everything
				logrus.WithFields(logrus.Fields{"port": port}).Debug("soweli | https succeeded")
				httpsCancel()
				httpCancel()
				upstreamPort = serverPort{
					port:  port,
					https: true,
				}
			}
		}()

		httpWg.Add(1)
		go func() {
			defer httpWg.Done()

			if testPortHTTP(httpCtx, dialer, upstream, port) {
				logrus.WithFields(logrus.Fields{"port": port}).Debug("soweli | http succeeded")
				httpCancel()
				// we want to make sure we're not racing with one of the https probes
				httpsWg.Wait()
				// then this can only race with http probes, which is fine since no preference for http hosts is guaranteed regardless
				if upstreamPort == (serverPort{}) {
					upstreamPort = serverPort{
						port:  port,
						https: false,
					}
				}
			}
		}()
	}

	httpsWg.Wait()
	httpWg.Wait()
	return upstreamPort, nil
}

func testPortHTTP(ctx context.Context, dialer *net.Dialer, upstream domainproxytypes.Upstream, port uint16) bool {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
		},
	}

	_, err := client.Head(fmt.Sprintf("http://%v:%v", upstream.IP, port))
	if err != nil {
		logrus.WithError(err).Error("soweli | testPortHTTP")
		return false
	} else {
		return true
	}
}

func testPortHTTPS(ctx context.Context, dialer *net.Dialer, upstream domainproxytypes.Upstream, port uint16) bool {
	serverName := ""
	if len(upstream.Names) > 0 {
		serverName = upstream.Names[0]
	}
	tlsDialer := &tls.Dialer{
		NetDialer: dialer,
		Config: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         serverName,
			VerifyConnection: func(tls.ConnectionState) error {
				return fmt.Errorf("abort connection")
			},
			VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
				return fmt.Errorf("abort connection")
			},
		},
	}

	host := upstream.IP.String()
	portStr := strconv.Itoa(int(port))

	conn, err := tlsDialer.DialContext(ctx, "tcp", net.JoinHostPort(host, portStr))
	if err == nil {
		conn.Close()
	} else {
		logrus.Debugf("soweli | testPortHTTPS: %v, %T, %T", err, err, errors.Unwrap(err))
		var recordHeaderErr tls.RecordHeaderError
		if errors.As(err, &recordHeaderErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, unix.ECONNREFUSED) {
			return false
		}
	}

	return true
}
*/

func (p *DomainTLSProxy) getOrProbeHost(dialer *net.Dialer, addr netip.Addr, upstream domainproxytypes.Upstream) (serverPort, error) {
	p.probedHostsMu.Lock()
	upstreamPort, ok := p.probedHosts[addr]
	p.probedHostsMu.Unlock()
	if !ok {
		logrus.Debug("probing host outside of nfqueue")

		var err error
		upstreamPort, err = p.probeHost(dialer, upstream)
		if err != nil {
			return serverPort{}, fmt.Errorf("probe host: %w", err)
		}
		if upstreamPort == (serverPort{}) {
			logrus.Debug("soweli | failed to dial")
			return serverPort{}, nil
		}
	}

	return upstreamPort, nil
}
