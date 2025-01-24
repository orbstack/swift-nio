package util

import (
	"context"

	"github.com/florianl/go-nfqueue"
	"github.com/mdlayher/netlink"
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

type NfqueueConfig struct {
	*nfqueue.Config
	Options          map[netlink.ConnOption]bool
	SetupHandler     func(*nfqueue.Nfqueue) error
	AttributeHandler func(context.Context, *nfqueue.Nfqueue, NfqueueLinkedAttribute)
	ErrorHandler     func(context.Context, error)
}

func RunNfqueue(ctx context.Context, config NfqueueConfig) error {
	queue, err := nfqueue.Open(config.Config)
	if err != nil {
		return err
	}

	for option, enabled := range config.Options {
		err = queue.SetOption(option, enabled)
		if err != nil {
			return err
		}
	}

	if config.SetupHandler != nil {
		err = config.SetupHandler(queue)
		if err != nil {
			return err
		}
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
		config.ErrorHandler(ctx, e)
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
