package qemulink

import (
	"encoding/binary"
	"os"
	"sync"

	"github.com/orbstack/macvirt/macvmgr/vnet/dglink/rawfile"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/bufferv2"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type endpoint struct {
	file       *os.File
	wg         sync.WaitGroup
	dispatcher stack.NetworkDispatcher
	macAddr    tcpip.LinkAddress
}

func New(file *os.File, macAddr tcpip.LinkAddress) (stack.LinkEndpoint, error) {
	e := &endpoint{
		file:    file,
		macAddr: macAddr,
	}
	err := unix.SetNonblock(int(file.Fd()), true)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (e *endpoint) Attach(dispatcher stack.NetworkDispatcher) {
	if e.dispatcher != nil || dispatcher == nil {
		return
	}
	e.wg.Add(1)
	e.dispatcher = dispatcher
	go func() { // S/R-SAFE: See above.
		e.dispatchLoop()
		e.dispatcher = nil
		e.wg.Done()
	}()
}

func (e *endpoint) IsAttached() bool {
	return e.dispatcher != nil
}

func (e *endpoint) MTU() uint32 {
	return 65520
}

func (e *endpoint) Capabilities() stack.LinkEndpointCapabilities {
	return stack.CapabilityRXChecksumOffload | stack.CapabilityTXChecksumOffload | stack.CapabilityResolutionRequired
}

func (e *endpoint) MaxHeaderLength() uint16 {
	return header.EthernetMinimumSize
}

func (e *endpoint) LinkAddress() tcpip.LinkAddress {
	return e.macAddr
}

func (e *endpoint) Wait() {
	e.wg.Wait()
}

func (e *endpoint) AddHeader(pkt stack.PacketBufferPtr) {
	eth := header.Ethernet(pkt.LinkHeader().Push(header.EthernetMinimumSize))
	eth.Encode(&header.EthernetFields{
		SrcAddr: pkt.EgressRoute.LocalLinkAddress,
		DstAddr: pkt.EgressRoute.RemoteLinkAddress,
		Type:    pkt.NetworkProtocolNumber,
	})
}

func (e *endpoint) writePacket(pkt stack.PacketBufferPtr) tcpip.Error {
	var fullBuf [65536]byte
	binary.LittleEndian.PutUint16(fullBuf[:2], uint16(pkt.Size()))
	slices := pkt.AsSlices()
	pos := 2
	for _, slice := range slices {
		copy(fullBuf[pos:], slice)
		pos += len(slice)
	}

	_, err := e.file.Write(fullBuf[:pos])
	if err == nil {
		return nil
	}

	return translateError(err)
}

func translateError(err error) tcpip.Error {
	if errno, ok := err.(unix.Errno); ok {
		return rawfile.TranslateErrno(errno)
	}

	return &tcpip.ErrInvalidEndpointState{}
}

func (e *endpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
	sentPackets := 0
	var err tcpip.Error
	for _, pkt := range pkts.AsSlice() {
		if err = e.writePacket(pkt); err != nil {
			break
		}
		sentPackets++
	}

	return sentPackets, err
}

func (e *endpoint) dispatchLoop() tcpip.Error {
	lenBuf := make([]byte, 2)
	buf := make([]byte, 65536)
	for {
		n, err := e.file.Read(lenBuf)
		if err != nil || n != 2 {
			return translateError(err)
		}

		pktLen := int(binary.LittleEndian.Uint16(lenBuf))
		pktBuf := buf[:pktLen]
		n, err = e.file.Read(pktBuf)
		if err != nil {
			return translateError(err)
		}

		if n != pktLen {
			return &tcpip.ErrInvalidEndpointState{}
		}

		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: bufferv2.MakeWithData(pktBuf),
		})
		defer pkt.DecRef()

		hdr, ok := pkt.LinkHeader().Consume(header.EthernetMinimumSize)
		if !ok {
			return &tcpip.ErrInvalidEndpointState{}
		}
		proto := header.Ethernet(hdr).Type()
		e.dispatcher.DeliverNetworkPacket(proto, pkt)
	}
}

func (e *endpoint) GSOMaxSize() uint32 {
	return 0
}

func (e *endpoint) SupportedGSO() stack.SupportedGSO {
	return stack.GSONotSupported
}

func (e *endpoint) ARPHardwareType() header.ARPHardwareType {
	return header.ARPHardwareEther
}

func (e *endpoint) Close() error {
	return e.file.Close()
}
