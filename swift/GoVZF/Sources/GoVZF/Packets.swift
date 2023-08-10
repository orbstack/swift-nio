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
private let IPPROTO_ICMPV6: UInt8 = 58

private let ICMPV6_NEIGHBOR_SOLICITATION: UInt8 = 135
private let ICMPV6_NEIGHBOR_ADVERTISEMENT: UInt8 = 136

private let ICMPV6_OPTION_SOURCE_LLADDR: UInt8 = 1
private let ICMPV6_OPTION_TARGET_LLADDR: UInt8 = 2

private let macAddrSize = 6
private let macAddrBroadcast: [UInt8] = [0xff, 0xff, 0xff, 0xff, 0xff, 0xff]
private let macAddrIpv4MulticastPrefix: [UInt8] = [0x01, 0x00, 0x5e]
private let macAddrIpv6MulticastPrefix: [UInt8] = [0x33, 0x33]
private let macAddrIpv6NdpMulticastPrefix: [UInt8] = [0x33, 0x33, 0xff]

// falls under scon machine /32
// fd07:b51a:cc66:0:b0c0:a617/96
private let nat64Prefix: [UInt8] = [0xfd, 0x07, 0xb5, 0x1a, 0xcc, 0x66, 0x00, 0x00, 0xa6, 0x17, 0xdb, 0x5e]
// da:9b:d0:54:e0:02
private let nat64MacAddrGuest: [UInt8] = [0xda, 0x9b, 0xd0, 0x54, 0xe0, 0x02]

typealias BrnetInterfaceIndex = UInt
let ifiBroadcast: BrnetInterfaceIndex = 0xffffffff

struct PacketWriteOptions {
    var sendDuplicate: Bool
}

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

    func store<T>(offset: Int, value: T) throws {
        if offset + MemoryLayout<T>.size > len {
            throw BrnetError.invalidPacket
        }

        data.storeBytes(of: value, toByteOffset: offset, as: T.self)
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
    private var hostRealMac: [UInt8]?
    private let allowMulticast: Bool

    init(realExternalMtu: Int = 1500, hostOverrideMac: [UInt8], allowMulticast: Bool = false) {
        self.realExternalMtu = realExternalMtu
        self.hostOverrideMac = hostOverrideMac
        self.allowMulticast = allowMulticast
    }

    /*
    INCOMING PACKET PROCESSING
    --------------------------
    1. rewrite destination MAC address from assigned host MAC to macOS
      - only if it equals the expected MAC for the interface

    (see below for MAC routing)
    */
    // warning: can be called concurrently! and multiple times per packet!
    func processToHost(pkt: Packet) throws -> PacketWriteOptions {
        // if we have actual host MAC...
        if let hostRealMac {
            // then check if we need to rewrite the destination MAC (Ethernet[0])
            let dstMacPtr = try pkt.slicePtr(offset: 0, len: macAddrSize)
            if memcmp(dstMacPtr, hostOverrideMac, macAddrSize) == 0 {
                // rewrite destination MAC (Ethernet[0])
                dstMacPtr.copyMemory(from: hostRealMac, byteCount: macAddrSize)
            }

            // also rewrite ARP destination MAC? (Ethernet + ARP[18])
            let etherType = pkt.etherType
            if etherType == ETHTYPE_ARP {
                let arpDstMacPtr = try pkt.slicePtr(offset: 14 + 18, len: macAddrSize)
                if memcmp(arpDstMacPtr, hostOverrideMac, macAddrSize) == 0 {
                    arpDstMacPtr.copyMemory(from: hostRealMac, byteCount: macAddrSize)
                }
            }

            // also rewrite IPv6 NDP destination MAC (Ethernet + IPv6 + NDP[8])
            /*
            Internet Control Message Protocol v6
                Type: Neighbor Advertisement (136)
                Code: 0
                Checksum: 0x8f04 [correct]
                [Checksum Status: Good]
                Flags: 0x60000000, Solicited, Override
                Target Address: fd07:b51a:cc66:1:0:242:ac11:2
                ICMPv6 Option (Target link-layer address : 02:42:ac:11:00:02)
                    Type: Target link-layer address (2)
                    Length: 1 (8 bytes)
                    Link-layer address: 02:42:ac:11:00:02 (02:42:ac:11:00:02)
             */
            if etherType == ETHTYPE_IPV6 {
                let nextHeader: UInt8 = try pkt.load(offset: 14 + 6)
                if nextHeader == IPPROTO_ICMPV6 {
                    let icmpv6Type: UInt8 = try pkt.load(offset: 14 + 40)
                    if icmpv6Type == ICMPV6_NEIGHBOR_SOLICITATION || icmpv6Type == ICMPV6_NEIGHBOR_ADVERTISEMENT {
                        // ICMPv6 Option (Target link-layer address)
                        // check for the option. not all packets have an option, and some have nonce
                        do {
                            let icmpv6OptionType: UInt8 = try pkt.load(offset: 14 + 40 + 24)
                            if icmpv6OptionType == ICMPV6_OPTION_SOURCE_LLADDR || icmpv6OptionType == ICMPV6_OPTION_TARGET_LLADDR {
                                let icmpv6DstMacPtr = try pkt.slicePtr(offset: 14 + 40 + 26, len: macAddrSize)
                                if memcmp(icmpv6DstMacPtr, hostOverrideMac, macAddrSize) == 0 {
                                    icmpv6DstMacPtr.copyMemory(from: hostRealMac, byteCount: macAddrSize)

                                    // fix checksum incrementally
                                    let oldChecksum = (try pkt.load(offset: 14 + 40 + 2) as UInt16).bigEndian
                                    let newChecksum = Checksum.update(oldChecksum: oldChecksum,
                                            oldData: hostOverrideMac, newData: hostRealMac)
                                    try pkt.store(offset: 14 + 40 + 2, value: newChecksum.bigEndian)
                                }
                            }
                        } catch {
                            // ignore if option not present
                        }
                    }
                }
            }
        }

        // check for duplicate-send heuristic for TCP ECN SYN->RST workaround
        var opts = PacketWriteOptions(sendDuplicate: false)
        do {
            opts.sendDuplicate = try PacketProcessor.needsDuplicateSend(pkt: pkt)
        } catch {
            // failed to parse packet - ignore
        }

        return opts
    }

    /*
    (below part is a static helper so VlanRouter can call it)
    2. map to interface (VlanRouter only)
      - extract index from dest MAC
        - should have DynBrnet prefix if unicast
      - if broadcast (ARP) or specific IPv6 multicast (NDP), send to all interfaces (ifiBroadcast)
      - drop other multicast. not supported - too hard to identify interface.
      - cannot use src MAC because it's a Docker container
     */
    static func extractInterfaceIndexToHost(pkt: Packet, macPrefix: [UInt8]) throws -> BrnetInterfaceIndex {
        // check if destination MAC matches prefix
        let dstMacPtr = try pkt.slicePtr(offset: 0, len: macAddrSize)
        if memcmp(dstMacPtr, macPrefix, macPrefix.count) == 0 {
            // extract interface index from destination MAC
            let dstMacLastByte = try pkt.load(offset: 0 + 5) as UInt8
            return BrnetInterfaceIndex(dstMacLastByte & 0x7f)
        }

        // no point in checking source MAC. it's either a Docker container or the Docker bridge.

        // if broadcast, then send it to everyone. we can't tell what the vlan is
        // TODO: consider ethertype top bits as vlan tag, via bpf xdp?
        if memcmp(dstMacPtr, macAddrBroadcast, macAddrBroadcast.count) == 0 {
            return ifiBroadcast
        }

        // also send it to everyone if it's ICMPv6 NDP multicast
        // NDP multicast ends with ffXX:XXXX where XX:XXXX is last 24 bits of IPv6 address
        // we don't know the assigned IPv6, so just match the FF part with the MAC (33:33:FF:XX:XX:XX)
        if memcmp(dstMacPtr, macAddrIpv6NdpMulticastPrefix, macAddrIpv6NdpMulticastPrefix.count) == 0 {
            return ifiBroadcast
        }

        // give up, drop packet
        // TODO support multicast?
        throw BrnetError.interfaceNotFound
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
        if hostRealMac == nil {
            // [concurrency] race doesn't matter - should all be the same, and ARC will free dupes
            hostRealMac = Array(UnsafeBufferPointer(start: srcMacPtr.assumingMemoryBound(to: UInt8.self), count: macAddrSize))
        }

        // allow IPv4 broadcast so ARP works
        // drop all IPv4 multicast (to save CPU from mDNS)
        // macOS broadcasts mDNS to v4 and v6 simultaneously. v6 is less likely to conflict, so just drop v4 to save CPU
        let dstMacPtr = try pkt.slicePtr(offset: 0, len: macAddrSize)
        if memcmp(dstMacPtr, macAddrIpv4MulticastPrefix, macAddrIpv4MulticastPrefix.count) == 0 {
            throw BrnetError.dropPacket
        }

        if !allowMulticast {
            // allow IPv6 multicast to NDP prefix (33:33:FF:XX:XX:XX) so NDP works
            // drop all other IPv6 multicast (to save CPU from mDNS)
            if memcmp(dstMacPtr, macAddrIpv6MulticastPrefix, macAddrIpv6MulticastPrefix.count) == 0 {
                let nextByte: UInt8 = try pkt.load(offset: 0 + 2)
                if nextByte != 0xff {
                    throw BrnetError.dropPacket
                }
            }
        }

        // rewrite source MAC (Ethernet[6])
        srcMacPtr.copyMemory(from: hostOverrideMac, byteCount: macAddrSize)

        // also rewrite ARP source MAC (Ethernet + ARP[8])
        let etherType = pkt.etherType
        if etherType == ETHTYPE_ARP {
            let arpSrcMacPtr = try pkt.slicePtr(offset: 14 + 8, len: macAddrSize)
            arpSrcMacPtr.copyMemory(from: hostOverrideMac, byteCount: macAddrSize)
        }

        // also rewrite IPv6 NDP source MAC (Ethernet + IPv6 + NDP[8])
        /*
        Internet Control Message Protocol v6
            Type: Neighbor Solicitation (135)
            Code: 0
            Checksum: 0x1aca [correct]
            [Checksum Status: Good]
            Reserved: 00000000
            Target Address: fd07:b51a:cc66:1:0:242:ac11:2
            ICMPv6 Option (Source link-layer address : be:d0:74:22:80:65)
                Type: Source link-layer address (1)
                Length: 1 (8 bytes)
                Link-layer address: be:d0:74:22:80:65 (be:d0:74:22:80:65)
         */
        if etherType == ETHTYPE_IPV6 {
            let nextHeader: UInt8 = try pkt.load(offset: 14 + 6)
            if nextHeader == IPPROTO_ICMPV6 {
                let icmpv6Type: UInt8 = try pkt.load(offset: 14 + 40)
                if icmpv6Type == ICMPV6_NEIGHBOR_SOLICITATION || icmpv6Type == ICMPV6_NEIGHBOR_ADVERTISEMENT {
                    // ICMPv6 Option (Source link-layer address)
                    // check for the option. not all packets have an option, and some have nonce
                    do {
                        let icmpv6OptionType: UInt8 = try pkt.load(offset: 14 + 40 + 24)
                        if icmpv6OptionType == ICMPV6_OPTION_SOURCE_LLADDR || icmpv6OptionType == ICMPV6_OPTION_TARGET_LLADDR {
                            let icmpv6SrcMacPtr = try pkt.slicePtr(offset: 14 + 40 + 26, len: macAddrSize)
                            if let hostRealMac,
                               memcmp(icmpv6SrcMacPtr, hostRealMac, macAddrSize) == 0 {
                                icmpv6SrcMacPtr.copyMemory(from: hostOverrideMac, byteCount: macAddrSize)

                                // fix checksum incrementally
                                let oldChecksum = (try pkt.load(offset: 14 + 40 + 2) as UInt16).bigEndian
                                let newChecksum = Checksum.update(oldChecksum: oldChecksum,
                                        oldData: hostRealMac, newData: hostOverrideMac)
                                try pkt.store(offset: 14 + 40 + 2, value: newChecksum.bigEndian)
                            }
                        }
                    } catch {
                        // ignore if option not present
                    }

                    // NAT64: respond to solicitation with advertisement for VM MAC
                    if icmpv6Type == ICMPV6_NEIGHBOR_SOLICITATION {
                        try maybeRespondNat64Ndp(pkt: pkt)
                    }
                }
            }
        }
    }

    func maybeRespondNat64Ndp(pkt: Packet) throws {
        // check target address prefix
        let targetAddrPtr = try pkt.slicePtr(offset: 14 + 40 + 8, len: 16)
        guard memcmp(targetAddrPtr, nat64Prefix, nat64Prefix.count) == 0 else {
            return
        }

        // copy the entire old packet, but skip the ethernet header
        let oldPacketBuf = [UInt8](UnsafeBufferPointer(start: pkt.data.advanced(by: 14).assumingMemoryBound(to: UInt8.self),
                count: pkt.len - 14))

        // 1. new dest MAC = src MAC
        let srcMacPtr = try pkt.slicePtr(offset: macAddrSize, len: macAddrSize)
        let dstMacPtr = try pkt.slicePtr(offset: 0, len: macAddrSize)
        dstMacPtr.copyMemory(from: srcMacPtr, byteCount: macAddrSize)

        // 2. new src MAC = guest MAC
        srcMacPtr.copyMemory(from: nat64MacAddrGuest, byteCount: macAddrSize)

        // 3. new dest IPv6 = src IPv6
        let srcIpv6Ptr = try pkt.slicePtr(offset: 14 + 8, len: 16)
        let dstIpv6Ptr = try pkt.slicePtr(offset: 14 + 24, len: 16)
        dstIpv6Ptr.copyMemory(from: srcIpv6Ptr, byteCount: 16)

        // 4. new src IP = target IP
        srcIpv6Ptr.copyMemory(from: targetAddrPtr, byteCount: 16)

        // 5. new ICMPv6 type = advertisement
        try pkt.store(offset: 14 + 40, value: ICMPV6_NEIGHBOR_ADVERTISEMENT)

        // flags
        try pkt.store(offset: 14 + 40 + 4, value: (0x60000000 as UInt32).bigEndian) // solicited, override

        // 6. new ICMPv6 option = target link-layer address
        do {
            try pkt.store(offset: 14 + 40 + 24, value: ICMPV6_OPTION_TARGET_LLADDR)
            let icmpv6TargetMacPtr = try pkt.slicePtr(offset: 14 + 40 + 26, len: macAddrSize)
            icmpv6TargetMacPtr.copyMemory(from: nat64MacAddrGuest, byteCount: macAddrSize)
        } catch {
            // ignore if option not present
        }

        // 7. new ICMPv6 checksum
        let oldChecksum = (try pkt.load(offset: 14 + 40 + 2) as UInt16).bigEndian
        // need to create [UInt8] from the buffers
        let newPacketBuf = [UInt8](UnsafeBufferPointer(start: pkt.data.advanced(by: 14).assumingMemoryBound(to: UInt8.self),
                count: pkt.len - 14))
        let newChecksum = Checksum.update(oldChecksum: oldChecksum,
                oldData: oldPacketBuf, newData: newPacketBuf)
        try pkt.store(offset: 14 + 40 + 2, value: newChecksum.bigEndian)

        // 8. redirect packet to host
        throw BrnetError.redirectToHost
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

    // Workaround for macOS retransmitting SYN if received RST, and ECN was enabled
    //
    // The problem: connecting to a not-listening port should return connection refused immediately,
    // but instead takes 1 second. macOS has a heuristic to retransmit SYN if it receives RST-ACK,
    // only if the connection is in setup stage with ECN enabled, and it's on a LOCAL interface.
    // Doesn't happen over Wi-Fi LAN b/c it only applies if route flag RTF_LOCAL is set.
    //
    // 2 possible fixes that require root:
    //   - set interface flag IFEF_ECN_DISABLE to disable ECN
    //   - unset route flag RTF_LOCAL to disable heuristic (will break route monitor)
    //
    // instead, we use a simple fix: send RST-ACK *twice* if it's in setup stage.
    // this works b/c first packet sets the flag that SYN-RST has been seen, and second one is obeyed.
    // https://github.com/apple-oss-distributions/xnu/blob/aca3beaa3dfbd42498b42c5e5ce20a938e6554e5/bsd/netinet/tcp_input.c#L3574
    //
    // this func checks for: TCP, RST-ACK from guest, in setup stage
    static func needsDuplicateSend(pkt: Packet) throws -> Bool {
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

        // TCP
        guard transportProto == IPPROTO_TCP else {
            return false
        }

        // flags = RST + ACK
        let tcpFlags = try pkt.load(offset: transportStartOff + 2+2+4+4+1) as UInt8
        guard tcpFlags == 0x14 else {
            return false
        }

        // setup stage:
        // seq = 1, win = 0. (ack is relative so it's harder to check)
        // should also check seq = 1 but it's misaligned
        //let tcpSeq = try pkt.load(offset: transportStartOff + 2+2) as UInt32
        let tcpWin = try pkt.load(offset: transportStartOff + 2+2+4+4+2) as UInt16
        guard tcpWin == 0 else {
            return false
        }

        // matches all heuristics
        return true
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
                    NSLog("[brnet] guest read error: \(errno)")
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
        // drop ref (breaks ref cycle to self in handler)
        source.setEventHandler(handler: nil)
    }

    deinit {
        iovs[0].iov_base.deallocate()
        // must free after data buf, so refs are valid
        iovs.deallocate()
    }
}

// internet checksum
private struct Checksum {
    // from gvisor
    private static func combine(_ a: UInt16, _ b: UInt16) -> UInt16 {
        let sum = UInt32(a) + UInt32(b)
        return UInt16((sum &+ (sum >> 16)) & 0xffff)
    }

    private static func incrementalUpdate(xsum: UInt16, old: UInt16, new: UInt16) -> UInt16 {
        combine(xsum, combine(new, ~old))
    }

    static func update(oldChecksum: UInt16, oldData: [UInt8], newData: [UInt8]) -> UInt16 {
        var checksum = ~oldChecksum
        var i = 0
        while i < oldData.count {
            checksum = incrementalUpdate(xsum: checksum,
                    old: (UInt16(oldData[i]) << 8) &+ UInt16(oldData[i + 1]),
                    new: (UInt16(newData[i]) << 8) &+ UInt16(newData[i + 1]))
            i += 2
        }
        return ~checksum
    }
}
