package domainproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/florianl/go-nfqueue"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/nftables"
	"github.com/mdlayher/netlink"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/domainproxy/sillytls"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/sys/unix"
)

func (d *DomainTLSProxy) startQueue(queueNum uint16) error {
	config := nfqueue.Config{
		NfQueue:      queueNum,
		MaxPacketLen: 65536,
		MaxQueueLen:  1024,
		Copymode:     nfqueue.NfQnlCopyPacket,
		Flags:        0,
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
					// there's an upstream, so accept the connection. (accept = continue nftables)
					// with GSO, we must always pass the payload back even if unmodified, or the GSO type breaks
					mark = d.cb.NfqueueMarkSkip(mark)
				} else {
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
		logrus.Debug("domaintlsproxy: probe did not find a port")
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

func writeTLSClientHello(w io.Writer, serverName string) error {
	hello := &sillytls.Handshake{
		Message: &sillytls.HandshakeClientHello{
			Version:   0x0303,
			Random:    []byte("OrbStackOrbStackOrbStackOrbStack"),
			SessionID: []byte("OrbStackOrbStackOrbStackOrbStack"),
			CipherSuites: []uint16{
				tls.TLS_AES_128_GCM_SHA256,
				tls.TLS_AES_256_GCM_SHA384,
				tls.TLS_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
				tls.TLS_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
				tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
				tls.TLS_RSA_WITH_RC4_128_SHA,
				tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
			},
			CompressionMethods:           []uint8{0x00},
			SupportedVersions:            []uint16{0x0304, 0x0303, 0x0302, 0x0301},
			ServerName:                   strings.TrimSuffix(serverName, "."),
			ALPNProtocols:                []string{"h2", "http/1.1"},
			SupportedGroups:              []sillytls.GroupID{tls.CurveP256, tls.CurveP384, tls.CurveP521, tls.X25519},
			SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.PKCS1WithSHA256, tls.PKCS1WithSHA384, tls.PKCS1WithSHA512, tls.PSSWithSHA256, tls.PSSWithSHA384, tls.PSSWithSHA512, tls.ECDSAWithP256AndSHA256, tls.ECDSAWithP384AndSHA384, tls.ECDSAWithP521AndSHA512, tls.Ed25519, tls.PKCS1WithSHA1, tls.ECDSAWithSHA1},
			KeyShares:                    []sillytls.KeyShare{},
		},
	}

	var b cryptobyte.Builder
	hello.Marshal(&b)
	helloBytes, err := b.Bytes()
	if err != nil {
		logrus.WithError(err).Error("soweli | testPortHTTPS: failed to marshal hello")
		return err
	}

	record := &sillytls.Record{
		LegacyVersion: 0x0301,
		ContentType:   hello.TLSRecordType(),
		Content:       helloBytes,
	}

	return record.Write(w)
}

func jsonPrettyPrint(v any) string {
	json, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(json)
}

func testPortHTTPS(ctx context.Context, dialer *net.Dialer, upstream domainproxytypes.Upstream, port uint16) bool {
	serverName := ""
	if len(upstream.Names) > 0 {
		serverName = upstream.Names[0]
	}

	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(upstream.IP.String(), strconv.Itoa(int(port))))
	if err != nil {
		logrus.WithError(err).Error("soweli | testPortHTTPS: failed to dial")
		return false
	}
	defer conn.Close()

	err = writeTLSClientHello(conn, serverName)
	if err != nil {
		logrus.WithError(err).Error("soweli | testPortHTTPS: failed to write hello")
		return false
	}
	logrus.Debug("soweli | testPortHTTPS: wrote hello")

	response := &sillytls.Record{}
	err = response.Read(conn)
	if err != nil {
		logrus.WithError(err).Error("soweli | testPortHTTPS: failed to read response")
		return false
	}
	logrus.Debug("soweli | testPortHTTPS: read response, type: ", response.ContentType)

	content := sillytls.GetRecordContentType(response.ContentType)
	if content == nil {
		logrus.Debug("soweli | testPortHTTPS: unknown content type")
		return false
	}

	contentString := cryptobyte.String(response.Content)
	err = content.Unmarshal(&contentString)
	if err != nil {
		logrus.WithError(err).Error("soweli | testPortHTTPS: failed to unmarshal content")
		return false
	}

	if handshake, ok := content.(*sillytls.Handshake); ok {
		logrus.Debugf("soweli | testPortHTTPS: %T=%s", content, jsonPrettyPrint(handshake))
	} else if alert, ok := content.(*sillytls.Alert); ok {
		logrus.Debugf("soweli | testPortHTTPS: %T=%s", content, alert.String())
	} else {
		logrus.Debugf("soweli | testPortHTTPS: %T", content)
	}

	return true
}

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
			logrus.Debug("domaintlsproxy: probe did not find a port")
			return serverPort{}, nil
		}
	}

	return upstreamPort, nil
}
