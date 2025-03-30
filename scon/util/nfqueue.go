package util

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/florianl/go-nfqueue"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/mdlayher/netlink"
	"github.com/sirupsen/logrus"
)

// nfqueue attribute linked to a queue
type NfqueueLinkedAttribute struct {
	nfqueue.Attribute
	queue *nfqueue.Nfqueue
}

func (a *NfqueueLinkedAttribute) SetVerdict(verdict int) error {
	return a.queue.SetVerdict(*a.PacketID, verdict)
}

func (a *NfqueueLinkedAttribute) SetVerdictBatch(verdict int) error {
	return a.queue.SetVerdictBatch(*a.PacketID, verdict)
}

func (a *NfqueueLinkedAttribute) SetVerdictModPacket(verdict int, packet []byte) error {
	return a.queue.SetVerdictModPacket(*a.PacketID, verdict, packet)
}

func (a *NfqueueLinkedAttribute) SetVerdictModPacketWithMark(verdict int, packet []byte, mark uint32) error {
	return a.queue.SetVerdictModPacketWithMark(*a.PacketID, verdict, int(mark), packet)
}

func (a *NfqueueLinkedAttribute) SetVerdictModPacketWithConnMark(verdict int, packet []byte, connMark uint32) error {
	return a.queue.SetVerdictModPacketWithConnMark(*a.PacketID, verdict, int(connMark), packet)
}

func (a *NfqueueLinkedAttribute) SetVerdictWithMark(verdict int, mark uint32) error {
	return a.queue.SetVerdictWithMark(*a.PacketID, verdict, int(mark))
}

func (a *NfqueueLinkedAttribute) SetVerdictWithConnMark(verdict int, connMark uint32) error {
	return a.queue.SetVerdictWithConnMark(*a.PacketID, verdict, int(connMark))
}

func (a *NfqueueLinkedAttribute) ParseIPs() (netip.Addr, netip.Addr, error) {
	// first 4 bits = IP version
	payload := *a.Payload
	if len(payload) < 1 {
		return netip.Addr{}, netip.Addr{}, errors.New("payload too short")
	}

	ipVersion := payload[0] >> 4
	var decoder gopacket.Decoder
	if ipVersion == 4 {
		decoder = layers.LayerTypeIPv4
	} else if ipVersion == 6 {
		decoder = layers.LayerTypeIPv6
	} else {
		return netip.Addr{}, netip.Addr{}, fmt.Errorf("unsupported ip version: %d", ipVersion)
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
		return netip.Addr{}, netip.Addr{}, errors.New("unsupported ip version")
	}
	if !ipOk1 || !ipOk2 {
		return netip.Addr{}, netip.Addr{}, errors.New("failed to get ip")
	}

	return srcIP, dstIP, nil
}

type NfqueueConfig struct {
	QueueNum         uint16
	AttributeHandler func(context.Context, *nfqueue.Nfqueue, NfqueueLinkedAttribute)
}

func RunNfqueue(ctx context.Context, config NfqueueConfig) error {
	ctx, cancel := context.WithCancel(ctx)

	queue, err := nfqueue.Open(&nfqueue.Config{
		NfQueue:      config.QueueNum,
		MaxPacketLen: 65536,
		MaxQueueLen:  1024,
		Copymode:     nfqueue.NfQnlCopyPacket,
		Flags:        0,
		Logger:       logrus.StandardLogger(),
	})
	if err != nil {
		return err
	}

	err = queue.SetOption(netlink.NoENOBUFS, true)
	if err != nil {
		return err
	}

	err = queue.RegisterWithErrorFunc(ctx, func(a nfqueue.Attribute) int {
		// handle packets in parallel to minimize happy eyeballs and load testing delays
		go config.AttributeHandler(ctx, queue, NfqueueLinkedAttribute{
			Attribute: a,
			queue:     queue,
		})
		// 0 = continue, else = stop read loop
		return 0
	}, func(e error) int {
		logrus.WithError(e).Error("nfqueue error")
		cancel()

		// 0 = continue, else = stop read loop
		return 0
	})
	if err != nil {
		return err
	}

	go func() {
		defer queue.Close()
		<-ctx.Done()
	}()

	return nil
}
