package cblink

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/orbstack/macvirt/vmgr/vnet/dglink/rawfile"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

var _ stack.LinkEndpoint = (*CallbackEndpoint)(nil)
var _ stack.GSOEndpoint = (*CallbackEndpoint)(nil)

type Callbacks interface {
	WritePacket(iovecs []unix.Iovec, totalLen int) int32
}

type CallbackEndpoint struct {
	cb Callbacks

	// mtu (maximum transmission unit) is the maximum size of a packet.
	mtu uint32

	// hdrSize specifies the link-layer header size. If set to 0, no header
	// is added/removed; otherwise an ethernet header is used.
	hdrSize int

	// addr is the address of the endpoint.
	addr tcpip.LinkAddress

	// caps holds the endpoint capabilities.
	caps stack.LinkEndpointCapabilities

	dispatcher stack.NetworkDispatcher

	// gsoMaxSize is the maximum GSO packet size. It is zero if GSO is
	// disabled.
	gsoMaxSize uint32

	// gsoKind is the supported kind of GSO.
	gsoKind stack.SupportedGSO
}

// Options specify the details about the fd-based endpoint to be created.
type Options struct {
	Callbacks Callbacks

	// MTU is the mtu to use for this endpoint.
	MTU uint32

	// EthernetHeader if true, indicates that the endpoint should read/write
	// ethernet frames instead of IP packets.
	EthernetHeader bool

	// Address is the link address for this endpoint. Only used if
	// EthernetHeader is true.
	Address tcpip.LinkAddress

	TXChecksumOffload bool
	RXChecksumOffload bool

	// GSOMaxSize is the maximum GSO packet size. It is zero if GSO is
	// disabled.
	GSOMaxSize uint32

	// GvisorGSOEnabled indicates whether Gvisor GSO is enabled or not.
	GvisorGSOEnabled bool
}

func New(opts *Options) (stack.LinkEndpoint, error) {
	caps := stack.LinkEndpointCapabilities(0)
	if opts.RXChecksumOffload {
		caps |= stack.CapabilityRXChecksumOffload
	}

	if opts.TXChecksumOffload {
		caps |= stack.CapabilityTXChecksumOffload
	}

	hdrSize := 0
	if opts.EthernetHeader {
		hdrSize = header.EthernetMinimumSize
		caps |= stack.CapabilityResolutionRequired
	}

	e := &CallbackEndpoint{
		cb:      opts.Callbacks,
		mtu:     opts.MTU,
		caps:    caps,
		addr:    opts.Address,
		hdrSize: hdrSize,
	}

	if opts.GSOMaxSize != 0 {
		if opts.GvisorGSOEnabled {
			e.gsoKind = stack.GVisorGSOSupported
		} else {
			e.gsoKind = stack.HostGSOSupported
		}
		e.gsoMaxSize = opts.GSOMaxSize
	}

	return e, nil
}

// Attach launches the goroutine that reads packets from the file descriptor and
// dispatches them via the provided dispatcher. If one is already attached,
// then nothing happens.
//
// Attach implements stack.LinkEndpoint.Attach.
func (e *CallbackEndpoint) Attach(dispatcher stack.NetworkDispatcher) {
	if e.dispatcher != nil {
		return
	}

	e.dispatcher = dispatcher
}

func (e *CallbackEndpoint) IsAttached() bool {
	return e.dispatcher != nil
}

func (e *CallbackEndpoint) MTU() uint32 {
	return e.mtu
}

func (e *CallbackEndpoint) Capabilities() stack.LinkEndpointCapabilities {
	return e.caps
}

func (e *CallbackEndpoint) MaxHeaderLength() uint16 {
	return uint16(e.hdrSize)
}

func (e *CallbackEndpoint) LinkAddress() tcpip.LinkAddress {
	return e.addr
}

func (e *CallbackEndpoint) SetLinkAddress(addr tcpip.LinkAddress) {
	panic("SetLinkAddress not supported")
}

// Wait implements stack.LinkEndpoint.Wait. It waits for the endpoint to stop
// reading from its FD.
func (e *CallbackEndpoint) Wait() {
}

// virtioNetHdrV1 is declared in linux/virtio_net.h.
type virtioNetHdrV1 struct {
	flags      uint8
	gsoType    uint8
	hdrLen     uint16
	gsoSize    uint16
	csumStart  uint16
	csumOffset uint16
	// v1 header (mergeable rx buffers)
	numBuffers uint16
}

const VirtioNetHdrSize = int(unsafe.Sizeof(virtioNetHdrV1{}))

// marshal serializes h to a newly-allocated byte slice, in little-endian byte
// order.
//
// Note: Virtio v1.0 onwards specifies little-endian as the byte ordering used
// for general serialization. This makes it difficult to use go-marshal for
// virtio types, as go-marshal implicitly uses the native byte ordering.
func (h *virtioNetHdrV1) marshal() []byte {
	buf := [VirtioNetHdrSize]byte{
		0: byte(h.flags),
		1: byte(h.gsoType),

		// Manually lay out the fields in little-endian byte order. Little endian =>
		// least significant bit goes to the lower address.

		2: byte(h.hdrLen),
		3: byte(h.hdrLen >> 8),

		4: byte(h.gsoSize),
		5: byte(h.gsoSize >> 8),

		6: byte(h.csumStart),
		7: byte(h.csumStart >> 8),

		8: byte(h.csumOffset),
		9: byte(h.csumOffset >> 8),

		10: byte(h.numBuffers),
		11: byte(h.numBuffers >> 8),
	}
	return buf[:]
}

// These constants are declared in linux/virtio_net.h.
const (
	_VIRTIO_NET_HDR_F_NEEDS_CSUM = 1
	_VIRTIO_NET_HDR_F_DATA_VALID = 2

	_VIRTIO_NET_HDR_GSO_TCPV4 = 1
	_VIRTIO_NET_HDR_GSO_TCPV6 = 4
)

func (e *CallbackEndpoint) AddHeader(pkt *stack.PacketBuffer) {
	if e.hdrSize > 0 {
		// Add ethernet header if needed.
		eth := header.Ethernet(pkt.LinkHeader().Push(header.EthernetMinimumSize))
		eth.Encode(&header.EthernetFields{
			SrcAddr: pkt.EgressRoute.LocalLinkAddress,
			DstAddr: pkt.EgressRoute.RemoteLinkAddress,
			Type:    pkt.NetworkProtocolNumber,
		})
	}
}

func (e *CallbackEndpoint) parseHeader(pkt *stack.PacketBuffer) bool {
	_, ok := pkt.LinkHeader().Consume(e.hdrSize)
	return ok

}

func (e *CallbackEndpoint) ParseHeader(pkt *stack.PacketBuffer) bool {
	if e.hdrSize > 0 {
		return e.parseHeader(pkt)
	}
	return true
}

// writePacket writes outbound packets to the file descriptor. If it is not
// currently writable, the packet is dropped.
func (e *CallbackEndpoint) writePacket(pkt *stack.PacketBuffer) tcpip.Error {
	// TODO: use pkt.Hash % numQueues for RSS
	var vnetHdrBuf []byte
	if e.gsoKind == stack.HostGSOSupported {
		vnetHdr := virtioNetHdrV1{}
		// GSO is only for large TCP packets. UDP and small TCP packets still need this part
		vnetHdr.flags = _VIRTIO_NET_HDR_F_DATA_VALID
		if pkt.GSOOptions.Type != stack.GSONone {
			vnetHdr.hdrLen = uint16(pkt.HeaderSize())
			if pkt.GSOOptions.NeedsCsum {
				// for some reason F_NEEDS_CSUM alone doesn't work
				vnetHdr.flags |= _VIRTIO_NET_HDR_F_NEEDS_CSUM
				vnetHdr.csumStart = header.EthernetMinimumSize + pkt.GSOOptions.L3HdrLen
				vnetHdr.csumOffset = pkt.GSOOptions.CsumOffset
			}
			if uint16(pkt.Data().Size()) > pkt.GSOOptions.MSS {
				switch pkt.GSOOptions.Type {
				case stack.GSOTCPv4:
					vnetHdr.gsoType = _VIRTIO_NET_HDR_GSO_TCPV4
				case stack.GSOTCPv6:
					vnetHdr.gsoType = _VIRTIO_NET_HDR_GSO_TCPV6
				default:
					panic(fmt.Sprintf("Unknown gso type: %v", pkt.GSOOptions.Type))
				}
				vnetHdr.gsoSize = pkt.GSOOptions.MSS
			}
		}
		vnetHdrBuf = vnetHdr.marshal()
	}

	views, offset := pkt.AsViewList()
	var skipped int
	var view *buffer.View
	for view = views.Front(); view != nil && offset >= view.Size(); view = view.Next() {
		offset -= view.Size()
		skipped++
	}

	// We've made it to the usable views.
	numIovecs := views.Len() - skipped
	if len(vnetHdrBuf) != 0 {
		numIovecs++
	}
	if numIovecs > rawfile.MaxIovs {
		numIovecs = rawfile.MaxIovs
	}

	// Allocate small iovec arrays on the stack.
	var iovecsArr [8]unix.Iovec
	iovecs := iovecsArr[:0]
	iovecs = rawfile.AppendIovecFromBytes(iovecs, vnetHdrBuf, numIovecs)
	// At most one slice has a non-zero offset.
	iovecs = rawfile.AppendIovecFromBytes(iovecs, view.AsSlice()[offset:], numIovecs)
	for view = view.Next(); view != nil; view = view.Next() {
		iovecs = rawfile.AppendIovecFromBytes(iovecs, view.AsSlice(), numIovecs)
	}

	ret := e.cb.WritePacket(iovecs[:numIovecs], len(vnetHdrBuf)+pkt.Size())
	if ret != 0 {
		// EPIPE = not started yet, or stopped
		// EAGAIN = queue is full
		// all other errors are unexpected
		errno := syscall.Errno(-ret)
		if errno != syscall.EPIPE && errno != syscall.EAGAIN {
			logrus.WithError(errno).Error("failed to write packet to VM")
		}

		return rawfile.TranslateErrno(errno)
	}

	return nil
}

// WritePackets writes outbound packets to the underlying file descriptors. If
// one is not currently writable, the packet is dropped.
//
// Being a batch API, each packet in pkts should have the following
// fields populated:
//   - pkt.EgressRoute
//   - pkt.GSOOptions
//   - pkt.NetworkProtocolNumber
func (e *CallbackEndpoint) WritePackets(pkts stack.PacketBufferList) (int, tcpip.Error) {
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

func (e *CallbackEndpoint) GSOMaxSize() uint32 {
	return e.gsoMaxSize
}

func (e *CallbackEndpoint) SupportedGSO() stack.SupportedGSO {
	return e.gsoKind
}

func (e *CallbackEndpoint) ARPHardwareType() header.ARPHardwareType {
	return header.ARPHardwareEther
}

func (e *CallbackEndpoint) Close() {}

func (e *CallbackEndpoint) InjectInbound(iovecs []unix.Iovec, totalLen int) unix.Errno {
	view := buffer.NewView(totalLen)
	for _, iov := range iovecs {
		bytes := unsafe.Slice((*byte)(iov.Base), int(iov.Len))
		_, err := view.Write(bytes)
		if err != nil {
			return unix.EIO
		}
	}

	buf := buffer.MakeWithView(view)
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buf,
	})
	defer pkt.DecRef()

	if e.hdrSize > 0 {
		hdr, ok := pkt.LinkHeader().Consume(e.hdrSize)
		if !ok {
			return unix.EINVAL
		}
		pkt.NetworkProtocolNumber = header.Ethernet(hdr).Type()
	}

	pkt.RXChecksumValidated = e.caps&stack.CapabilityRXChecksumOffload != 0
	dispatcher := e.dispatcher
	if dispatcher == nil {
		return unix.EPIPE
	}

	dispatcher.DeliverNetworkPacket(pkt.NetworkProtocolNumber, pkt)

	return 0
}
