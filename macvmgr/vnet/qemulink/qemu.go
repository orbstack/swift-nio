package qemulink

import (
	"encoding/binary"
	"internal/poll"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/dgramlink/rawfile"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type endpoint struct {
	pfd      poll.FD
	wg       sync.WaitGroup
	attached bool
	macAddr  tcpip.LinkAddress
}

func New(fd int, macAddr tcpip.LinkAddress) (stack.LinkEndpoint, error) {
	e := &endpoint{
		pfd: poll.FD{
			Sysfd:         fd,
			IsStream:      true,
			ZeroReadIsEOF: true,
		},
		macAddr: macAddr,
	}
	err := unix.SetNonblock(fd, true)
	if err != nil {
		return nil, err
	}
	err = e.pfd.Init("tcp", true)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (e *endpoint) Attach(dispatcher stack.NetworkDispatcher) {
	if e.attached {
		return
	}
	e.wg.Add(1)
	e.attached = true
	go func() { // S/R-SAFE: See above.
		e.dispatchLoop()
		e.attached = false
		e.wg.Done()
	}()
}

func (e *endpoint) IsAttached() bool {
	return e.attached
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
	var lenBuf [2]byte
	binary.LittleEndian.PutUint16(lenBuf[:], uint16(pkt.Size()))

	var bufsArr [8][]byte
	bufsArr[0] = lenBuf[:]
	bufs := bufsArr[:1]
	bufs = append(bufs, pkt.AsSlices()...)

	_, err := e.pfd.Writev(&bufs)
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
		n, err := e.pfd.Read(lenBuf)
		if err != nil || n != 2 {
			return translateError(err)
		}

		pktLen := int(binary.LittleEndian.Uint16(lenBuf))
		pktBuf := buf[:pktLen]
		n, err = e.pfd.Read(pktBuf)
		if err != nil {
			return translateError(err)
		}
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
	return e.pfd.Close()
}
