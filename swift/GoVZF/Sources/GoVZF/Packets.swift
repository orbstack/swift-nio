//
// Created by Danny Lin on 6/3/23.
//

import Foundation
import vmnet
import CBridge

private let VIRTIO_NET_HDR_F_NEEDS_CSUM: UInt8 = 1 << 0
private let VIRTIO_NET_HDR_F_DATA_VALID: UInt8 = 1 << 1

private let VIRTIO_NET_HDR_GSO_NONE: UInt8 = 0
private let VIRTIO_NET_HDR_GSO_TCPV4: UInt8 = 1
private let VIRTIO_NET_HDR_GSO_TCPV6: UInt8 = 4

private let ETHTYPE_IPV4: UInt16 = 0x0800
private let ETHTYPE_IPV6: UInt16 = 0x86DD
private let ETHTYPE_ARP: UInt16 = 0x0806

private let IPPROTO_UDP: UInt8 = 17
private let IPPROTO_TCP: UInt8 = 6

private let macAddrSize = 6

typealias BrnetInterfaceIndex = Int

struct Packet {
    let data: UnsafeMutableRawPointer
    let len: Int

    init(desc: vmpktdesc) {
        data = desc.vm_pkt_iov[0].iov_base
        len = desc.vm_pkt_size
    }

    init(iov: UnsafeMutablePointer<iovec>, len: Int) {
        data = iov[0].iov_base!
        self.len = len
    }

    func load<T>(offset: Int) throws -> T {
        if offset + MemoryLayout<T>.size > len {
            throw BrnetError.invalidPacket
        }

        return data.load(fromByteOffset: offset, as: T.self)
    }

    func slicePtr(offset: Int, len: Int) throws -> UnsafeMutableRawPointer {
        // bounds check
        if offset + len > self.len {
            throw BrnetError.invalidPacket
        }

        return data.advanced(by: offset)
    }

    var etherType: UInt16 {
        do {
            return (try load(offset: 12) as UInt16).bigEndian
        } catch {
            // fallback: invalid type - causes passthrough in packet processor
            return 0
        }
    }
}

class PacketProcessor {
    // MTU that we're *supposed* to use if going out to a real network
    private let realExternalMtu: Int
    // the host MAC we use with the VM
    private let hostOverrideMac: [UInt8]
    // the host MAC that macOS expects to see
    private var hostActualMac: [UInt8]?

    init(realExternalMtu: Int = 1500, hostOverrideMac: [UInt8]) {
        self.realExternalMtu = realExternalMtu
        self.hostOverrideMac = hostOverrideMac
    }

    /*
    INCOMING PACKET PROCESSING
    --------------------------
    1. rewrite destination MAC address from assigned host MAC to macOS
      - only if it equals the expected MAC for the interface
    2. map to interface
      - extract index from src MAC
        - to get vmnet interface
        - should have DynBrnet prefix
        - this covers broadcast and multicast cases: src MAC is always present
    */
    // warning: can be called concurrently!
    @discardableResult
    func processToHost(pkt: Packet) throws -> BrnetInterfaceIndex {
        // if we have actual host MAC...
        if let hostActualMac {
            // then check if we need to rewrite the destination MAC (Ethernet[0])
            let dstMacPtr = try pkt.slicePtr(offset: 0, len: macAddrSize)
            if memcmp(dstMacPtr, hostOverrideMac, macAddrSize) == 0 {
                // rewrite destination MAC (Ethernet[0])
                dstMacPtr.copyMemory(from: hostActualMac, byteCount: macAddrSize)
            }

            // also rewrite ARP destination MAC? (Ethernet + ARP[18])
            let etherType = pkt.etherType
            if etherType == ETHTYPE_ARP {
                let arpDstMacPtr = try pkt.slicePtr(offset: 14 + 18, len: macAddrSize)
                if memcmp(arpDstMacPtr, hostOverrideMac, macAddrSize) == 0 {
                    arpDstMacPtr.copyMemory(from: hostActualMac, byteCount: macAddrSize)
                }
            }
        }

        // mask out and return the interface index:
        // lower 7 bits of the last octet
        let srcMacLastByte = try pkt.load(offset: 6 + 5) as UInt8
        return BrnetInterfaceIndex(srcMacLastByte & 0x7f)
    }

    /*
    OUTGOING PACKET PROCESSING
    --------------------------
    1. build vnet hdr
      - reconstruct checksum and TSO metadata from packet
    2. rewrite source MAC address from macOS to match assigned host MAC
      - must do this because macOS doesn't let us change the vmnet bridge100's MAC addr
    */
    // warning: can be called concurrently!
    func processToGuest(pkt: Packet) throws {
        // save the actual macOS source MAC if needed (for later translation) - Ethernet[6]
        let srcMacPtr = try pkt.slicePtr(offset: macAddrSize, len: macAddrSize)
        if hostActualMac == nil {
            // [concurrency] race doesn't matter - should all be the same, and ARC will free dupes
            hostActualMac = Array(UnsafeBufferPointer(start: srcMacPtr.assumingMemoryBound(to: UInt8.self), count: macAddrSize))
        }

        // rewrite source MAC (Ethernet[6])
        srcMacPtr.copyMemory(from: hostOverrideMac, byteCount: macAddrSize)

        // also rewrite ARP source MAC (Ethernet + ARP[8])
        let etherType = pkt.etherType
        if etherType == ETHTYPE_ARP {
            let arpSrcMacPtr = try pkt.slicePtr(offset: 14 + 8, len: macAddrSize)
            arpSrcMacPtr.copyMemory(from: hostOverrideMac, byteCount: macAddrSize)
        }
    }

    func buildVnetHdr(pkt: Packet) throws -> virtio_net_hdr {
        var hdr = virtio_net_hdr()
        hdr.flags = VIRTIO_NET_HDR_F_DATA_VALID

        // read ethertype from pkt
        let ipStartOff = 14
        let etherType = pkt.etherType
        // read udp/tcp
        var transportProto: UInt8 = 0
        var ipHdrLen = 0
        if etherType == ETHTYPE_IPV4 {
            //print("ipv4")
            transportProto = try pkt.load(offset: ipStartOff + 9)
            // not always 20 bytes
            ipHdrLen = Int(((try pkt.load(offset: ipStartOff) as UInt8) & 0x0F) * 4)
        } else if etherType == ETHTYPE_IPV6 {
            //print("ipv6")
            let nextHeader: UInt8 = try pkt.load(offset: ipStartOff + 6)
            // handle hop-by-hop extension header
            if nextHeader == 0 {
                //print("hop-by-hop")
                transportProto = try pkt.load(offset: ipStartOff + 40)
                ipHdrLen = 40 + 8
            } else {
                transportProto = nextHeader
                ipHdrLen = 40
            }
        }
        let transportStartOff = ipStartOff + ipHdrLen
        //print("etherType: \(String(etherType, radix: 16))")
        //print("transportProto: \(String(transportProto, radix: 16))")
        //print("ipHdrLen: \(ipHdrLen)")
        //print("transportStartOff: \(transportStartOff)")

        // csum: for TCP and UDP
        var transportHdrLen = 0
        if transportProto == IPPROTO_TCP {
            //print("tcp")
            hdr.flags |= VIRTIO_NET_HDR_F_NEEDS_CSUM
            hdr.csum_start = UInt16(transportStartOff)
            hdr.csum_offset = UInt16(16)
            //print("csum start: \(hdr.csum_start)")
            //print("csum offset: \(hdr.csum_offset)")
        } else if transportProto == IPPROTO_UDP {
            //print("udp")
            hdr.flags |= VIRTIO_NET_HDR_F_NEEDS_CSUM
            hdr.csum_start = UInt16(transportStartOff)
            hdr.csum_offset = UInt16(6)
            //print("csum start: \(hdr.csum_start)")
            //print("csum offset: \(hdr.csum_offset)")
            transportHdrLen = 8
        }

        // gso: if TCP data segment > MSS (1500 - IP - TCP)
        if transportProto == IPPROTO_TCP {
            let tcpHdrLen = ((try pkt.load(offset: transportStartOff + 12) as UInt8) >> 4) * 4
            let tcpDataLen = pkt.len - transportStartOff - Int(tcpHdrLen)
            let tcpMss = realExternalMtu - ipHdrLen - Int(tcpHdrLen)
            //print("tcp hdr len: \(tcpHdrLen)")
            //print("tcp data len: \(tcpDataLen)")
            //print("tcp mss: \(tcpMss)")
            if tcpDataLen > tcpMss {
                //print("tcp GSO > MSS")
                if etherType == ETHTYPE_IPV4 {
                    hdr.gso_type = UInt8(VIRTIO_NET_HDR_GSO_TCPV4)
                } else if etherType == ETHTYPE_IPV6 {
                    hdr.gso_type = UInt8(VIRTIO_NET_HDR_GSO_TCPV6)
                }
                hdr.gso_size = UInt16(tcpMss)
                //print("gso type: \(hdr.gso_type)")
                //print("gso size: \(hdr.gso_size)")
            }

            transportHdrLen = Int(tcpHdrLen)
        }

        // hdr_size is just a performance hint
        // it's the sum of all headers, including ethernet + ip + transport
        hdr.hdr_len = UInt16(transportStartOff + transportHdrLen)

        return hdr
    }
}

// generic guest fd reader that takes a packet callback
class GuestReader {
    private let source: DispatchSourceRead
    private let iovs: UnsafeMutablePointer<iovec>

    init(guestFd: Int32, maxPacketSize: UInt64,
         onPacket: @escaping (UnsafeMutablePointer<iovec>, Int) -> Void) {
        iovs = UnsafeMutablePointer<iovec>.allocate(capacity: 1)
        iovs[0].iov_base = UnsafeMutableRawPointer.allocate(byteCount: Int(maxPacketSize), alignment: 1)

        source = DispatchSource.makeReadSource(fileDescriptor: guestFd, queue: vmnetQueue)
        source.setEventHandler { [self] in
            // read from
            let buf = iovs[0].iov_base!
            let n = read(guestFd, buf, Int(maxPacketSize))
            guard n > 0 else {
                if errno != EAGAIN && errno != EWOULDBLOCK {
                    NSLog("[brnet] tap read error: \(errno)")
                }
                return
            }

            // set in iov
            iovs[0].iov_len = n

            // dispatch
            onPacket(iovs, n)
        }
        source.activate()
    }

    func close() {
        // remove callbacks
        source.cancel()
        // drop ref
        source.setEventHandler(handler: nil)
    }

    deinit {
        iovs[0].iov_base.deallocate()
        // must free after data buf, so refs are valid
        iovs.deallocate()
    }
}