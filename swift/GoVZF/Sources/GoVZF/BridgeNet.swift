//
//  BridgeNet.swift
//  GoVZF
//
//  Created by Danny Lin on 5/13/23.
//

import Foundation
import vmnet
import CBridge

// vmnet is ok with concurrent queue
// gets us from 21 -> 30 Gbps
let vmnetQueue = DispatchQueue(label: "dev.kdrag0n.govzf.bridge", attributes: .concurrent)

private let dgramSockBuf = 512 * 1024
private let maxPacketsPerRead = 64

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

private enum VmnetError: Error {
    case generalFailure
    case memFailure
    case invalidArgument
    case setupIncomplete
    case invalidAccess
    case packetTooBig
    case bufferExhausted
    case tooManyPackets
    case sharingServiceBusy

    case noInterfaceRef

    static func from(_ status: vmnet_return_t) -> VmnetError {
        switch status {
        case .VMNET_FAILURE:
            return .generalFailure
        case .VMNET_MEM_FAILURE:
            return .memFailure
        case .VMNET_INVALID_ARGUMENT:
            return .invalidArgument
        case .VMNET_SETUP_INCOMPLETE:
            return .setupIncomplete
        case .VMNET_INVALID_ACCESS:
            return .invalidAccess
        case .VMNET_PACKET_TOO_BIG:
            return .packetTooBig
        case .VMNET_BUFFER_EXHAUSTED:
            return .bufferExhausted
        case .VMNET_TOO_MANY_PACKETS:
            return .tooManyPackets
        case .VMNET_SHARING_SERVICE_BUSY:
            return .sharingServiceBusy
        default:
            return .generalFailure
        }
    }
}

private enum BridgeError: Error {
    case errno(Int32)
    case invalidPacket
}

private func vmnetStartInterface(ifDesc: xpc_object_t, queue: DispatchQueue) throws -> (interface_ref, xpc_object_t) {
    let sem = DispatchSemaphore(value: 0)
    var outIfParam: xpc_object_t?
    var outStatus: vmnet_return_t = .VMNET_FAILURE

    let interfaceRef = vmnet_start_interface(ifDesc, vmnetQueue) { (status, ifParam) in
        outStatus = status
        outIfParam = ifParam
        sem.signal()
    }

    guard let interfaceRef else {
        throw VmnetError.noInterfaceRef
    }

    sem.wait()
    guard outStatus == .VMNET_SUCCESS, let outIfParam else {
        throw VmnetError.from(outStatus)
    }

    return (interfaceRef, outIfParam)
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
            throw BridgeError.invalidPacket
        }

        return data.load(fromByteOffset: offset, as: T.self)
    }

    func slicePtr(offset: Int, len: Int) throws -> UnsafeMutableRawPointer {
        // bounds check
        if offset + len > self.len {
            throw BridgeError.invalidPacket
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

struct BridgeNetworkConfig: Codable {
    var guestFd: Int32
    let shouldReadGuest: Bool

    let uuid: String
    let ip4Address: String
    let ip4Mask: String
    let ip6Address: String?
    let hostOverrideMac: [UInt8]

    let maxLinkMtu: Int
}

class BridgeNetwork {
    let config: BridgeNetworkConfig

    private let ifRef: interface_ref
    private let processor: PacketProcessor
    private var guestReader: GuestReader? = nil
    private let hostReadIovs: UnsafeMutablePointer<iovec>
    private let vnetHdr: UnsafeMutablePointer<virtio_net_hdr>

    init(config: BridgeNetworkConfig) throws {
        self.config = config
        self.processor = PacketProcessor(hostOverrideMac: config.hostOverrideMac)

        let ifDesc = xpc_dictionary_create(nil, nil, 0)
        xpc_dictionary_set_uint64(ifDesc, vmnet_operation_mode_key, UInt64(operating_modes_t.VMNET_HOST_MODE.rawValue))
        // macOS max MTU = 16384, but we use TSO instead and match VM bridge MTU
        xpc_dictionary_set_uint64(ifDesc, vmnet_mtu_key, 1500)

        // UUID
        let uuid = UUID(uuidString: config.uuid)!
        var uuidBytes = [UInt8](repeating: 0, count: 16)
        (uuid as NSUUID).getBytes(&uuidBytes)
        xpc_dictionary_set_uuid(ifDesc, vmnet_interface_id_key, uuidBytes)

        xpc_dictionary_set_uuid(ifDesc, vmnet_network_identifier_key, uuidBytes)
        xpc_dictionary_set_string(ifDesc, vmnet_host_ip_address_key, config.ip4Address)
        xpc_dictionary_set_string(ifDesc, vmnet_host_subnet_mask_key, config.ip4Mask)
        if let ip6Address = config.ip6Address {
            xpc_dictionary_set_string(ifDesc, vmnet_host_ipv6_address_key, ip6Address)
        }
        /* vmnet_start_address_key, vmnet_end_address_key, vmnet_subnet_mask_key are for shared/NAT */

        // use our own MAC address (allow any)
        xpc_dictionary_set_bool(ifDesc, vmnet_allocate_mac_address_key, false)
        // sets IFBIF_PRIVATE
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_isolation_key, true)

        // TSO and checksum offload
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_checksum_offload_key, true)
        // enable TSO if link MTU allows
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_tso_key, config.maxLinkMtu >= 65535)

        let (_ifRef, ifParam) = try vmnetStartInterface(ifDesc: ifDesc, queue: vmnetQueue)
        self.ifRef = _ifRef
        //print("if param: \(ifParam)")
        let maxPacketSize = xpc_dictionary_get_uint64(ifParam, vmnet_max_packet_size_key)
        // vnet header only if mtu = max
        let needsVnetHdr = maxPacketSize >= 65535

        // pre-allocate buffers
        // theoretically: max 200 packets, but 65k*200 is big so limit it
        hostReadIovs = UnsafeMutablePointer<iovec>.allocate(capacity: maxPacketsPerRead)
        var pktDescs = [vmpktdesc]()
        pktDescs.reserveCapacity(maxPacketsPerRead)

        // update iov pointers
        for i in 0..<maxPacketsPerRead {
            // allocate buf and set in iov
            hostReadIovs[i].iov_base = UnsafeMutableRawPointer.allocate(byteCount: Int(maxPacketSize), alignment: 1)
            hostReadIovs[i].iov_len = Int(maxPacketSize)

            // set in pktDesc
            let pktDesc = vmpktdesc(vm_pkt_size: Int(maxPacketSize),
                vm_pkt_iov: hostReadIovs.advanced(by: i),
                vm_pkt_iovcnt: 1,
                vm_flags: 0)
            pktDescs.append(pktDesc)
        }

        // vnet header buffer for outgoing packets
        vnetHdr = UnsafeMutablePointer<virtio_net_hdr>.allocate(capacity: 1)

        // must keep self ref to prevent deinit while referenced
        let ret = vmnet_interface_set_event_callback(ifRef, .VMNET_INTERFACE_PACKETS_AVAILABLE, vmnetQueue) { [self] (eventMask, event) in
            //print("num packets: \(xpc_dictionary_get_uint64(event, vmnet_estimated_packets_available_key))")

            // read as many packets as we can
            var pktsRead = Int32(maxPacketsPerRead) // max
            let ret = vmnet_read(ifRef, &pktDescs, &pktsRead)
            guard ret == .VMNET_SUCCESS else {
                NSLog("[brnet] read error: \(VmnetError.from(ret))")
                return
            }

            // send packets to tap
            for i in 0..<Int(pktsRead) {
                let pktDesc = pktDescs[i]

                // sanity: never write a packet > 65535 bytes. that breaks the network, so just drop it
                let vnetHdrSize = needsVnetHdr ? MemoryLayout<virtio_net_hdr>.size : 0
                guard pktDesc.vm_pkt_size + vnetHdrSize <= 65535 else {
                    //print("packet too big: \(pktDesc.vm_pkt_size + vnetHdrSize)")
                    continue
                }

                let pkt = Packet(desc: pktDesc)
                do {
                    try processor.processToGuest(pkt: pkt)
                    vnetHdr[0] = try processor.buildVnetHdr(pkt: pkt)
                } catch {
                    NSLog("[brnet] error processing/building hdr: \(error)")
                    continue
                }
                var iovs = [
                    iovec(iov_base: vnetHdr, iov_len: vnetHdrSize),
                    iovec(iov_base: pkt.data, iov_len: pkt.len)
                ]
                let totalSize = pkt.len + vnetHdrSize
                //print("writing \(totalSize) bytes to tap")
                let ret = writev(config.guestFd, &iovs, 2)
                guard ret == totalSize else {
                    if errno != ENOBUFS {
                        NSLog("[brnet] write error: \(errno)")
                    }
                    continue
                }
            }

            // reset descs
            for i in 0..<Int(pktsRead) {
                pktDescs[i].vm_pkt_size = Int(maxPacketSize)
                pktDescs[i].vm_pkt_iovcnt = 1
                pktDescs[i].vm_flags = 0
            }
        }
        guard ret == .VMNET_SUCCESS else {
            // dealloc
            for i in 0..<maxPacketsPerRead {
                hostReadIovs[i].iov_base?.deallocate()
            }
            hostReadIovs.deallocate()
            vnetHdr.deallocate()

            throw VmnetError.from(ret)
        }

        // read from guest, write to vmnet
        if config.shouldReadGuest {
            guestReader = GuestReader(guestFd: config.guestFd, maxPacketSize: maxPacketSize,
                    onPacket: { [self] iov, len in
                        let pkt = Packet(iov: iov, len: len)
                        do {
                            let index = try processor.processToHost(pkt: pkt)
                        } catch {
                            NSLog("[brnet] error processing to host: \(error)")
                            return
                        }
                        tryWriteToHost(iov: iov, len: len)
                    })
        }
    }

    func tryWriteToHost(iov: UnsafeMutablePointer<iovec>, len: Int) {
        // write to vmnet
        var pktDesc = vmpktdesc(vm_pkt_size: len,
                vm_pkt_iov: iov,
                vm_pkt_iovcnt: 1,
                vm_flags: 0)
        var pktsWritten = Int32(1)
        let ret2 = vmnet_write(ifRef, &pktDesc, &pktsWritten)
        guard ret2 == .VMNET_SUCCESS else {
            NSLog("[brnet] vmnet write error: \(VmnetError.from(ret2))")
            return
        }
    }

    func close() {
        // remove callbacks
        if let guestReader {
            guestReader.close()
        }
        var ret = vmnet_interface_set_event_callback(ifRef, .VMNET_INTERFACE_PACKETS_AVAILABLE, nil, nil)
        if ret != .VMNET_SUCCESS {
            NSLog("[brnet] remove callback error: \(VmnetError.from(ret))")
        }

        // drain packets w/ barrier
        let sem = DispatchSemaphore(value: 0)
        ret = vmnetQueue.sync(flags: .barrier) {
            // sem still needed to wait for vmnet_stop_interface completion
            return vmnet_stop_interface(ifRef, vmnetQueue) { status in
                if status != .VMNET_SUCCESS {
                    NSLog("[brnet] stop status: \(VmnetError.from(status))")
                }
                sem.signal()
            }
        }
        // wait outside the barrier in order to avoid deadlock
        guard ret == .VMNET_SUCCESS else {
            NSLog("[brnet] stop error: \(VmnetError.from(ret))")
            return
        }
        sem.wait()
    }

    deinit {
        // guestReader will get deinited too
        // safe to deallocate now that refs from callbacks are gone
        for i in 0..<maxPacketsPerRead {
            hostReadIovs[i].iov_base.deallocate()
        }
        hostReadIovs.deallocate()
    }
}

@_cdecl("swext_brnet_create")
func swext_brnet_create(configJsonStr: UnsafePointer<CChar>) -> UnsafeMutablePointer<GovzfResultCreate> {
    let configJson = String(cString: configJsonStr)
    let config = try! JSONDecoder().decode(BridgeNetworkConfig.self, from: configJson.data(using: .utf8)!)

    let result = ResultWrapper<GovzfResultCreate>()
    do {
        let obj = try BridgeNetwork(config: config)
        // take a long-lived ref for Go
        let ptr = Unmanaged.passRetained(obj).toOpaque()
        result.set(GovzfResultCreate(ptr: ptr, err: nil, rosetta_canceled: false))
    } catch {
        let prettyError = "\(error)"
        result.set(GovzfResultCreate(ptr: nil, err: strdup(prettyError.cString(using: .utf8)!), rosetta_canceled: false))
    }

    return result.waitPtr()
}

@_cdecl("swext_brnet_close")
func swext_brnet_close(ptr: UnsafeMutableRawPointer) {
    // ref is dropped at the end of this function
    let obj = Unmanaged<BridgeNetwork>.fromOpaque(ptr).takeRetainedValue()
    obj.close()
}
