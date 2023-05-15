//
//  BridgeNet.swift
//  GoVZF
//
//  Created by Danny Lin on 5/13/23.
//

import Foundation
import vmnet

// vmnet is ok with concurrenet queue
// gets us from 21 -> 30 Gbps
private let queue = DispatchQueue(label: "dev.kdrag0n.govzf.bridge", attributes: .concurrent)

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

    let interfaceRef = vmnet_start_interface(ifDesc, queue) { (status, ifParam) in
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

private func buildVnetHdr(pkt: UnsafeMutableRawPointer, pktLen: Int, realMtu: Int = 1500) throws -> virtio_net_hdr {
    var hdr = virtio_net_hdr()
    hdr.flags = VIRTIO_NET_HDR_F_DATA_VALID

    func checkLoad<T>(offset: Int) throws -> T {
        if offset + MemoryLayout<T>.size > pktLen {
            throw BridgeError.invalidPacket
        }

        return pkt.load(fromByteOffset: offset, as: T.self)
    }

    // read ethertype from pkt
    let ipStartOff = 14
    let etherType = (try checkLoad(offset: 12) as UInt16).bigEndian
    // read udp/tcp
    var transportProto: UInt8 = 0
    var transportHdrLen = 0
    if etherType == ETHTYPE_IPV4 {
        //print("ipv4")
        transportProto = try checkLoad(offset: ipStartOff + 9)
        // not always 20 bytes
        transportHdrLen = Int(((try checkLoad(offset: ipStartOff) as UInt8) & 0x0F) * 4)
    } else if etherType == ETHTYPE_IPV6 {
        //print("ipv6")
        transportProto = try checkLoad(offset: ipStartOff + 6)
        // assume 40 bytes for now
        // TODO: check for hop-by-hop extension headers
        transportHdrLen = 40
    }
    let transportStartOff = ipStartOff + transportHdrLen
    //print("etherType: \(String(etherType, radix: 16))")
    //print("transportProto: \(String(transportProto, radix: 16))")
    //print("transportHdrLen: \(transportHdrLen)")
    //print("transportStartOff: \(transportStartOff)")

    // csum: for TCP and UDP
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
    }

    // gso: if TCP data segment > MSS (1500 -
    if transportProto == IPPROTO_TCP {
        let tcpHdrLen = ((try checkLoad(offset: transportStartOff + 12) as UInt8) >> 4) * 4
        let tcpDataLen = pktLen - transportStartOff - Int(tcpHdrLen)
        let tcpMss = realMtu - transportHdrLen - Int(tcpHdrLen)
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
    }

    return hdr
}

struct BridgeNetworkConfig: Codable {
    var tapFd: Int32

    let uuid: String
    let ip4Address: String
    let ip4Mask: String
    let ip6Address: String?
    let maxLinkMtu: Int
}

class BridgeNetwork {
    let config: BridgeNetworkConfig
    private let ifRef: interface_ref
    private let fdReadSource: DispatchSourceRead
    private let fdReadIovs: UnsafeMutablePointer<iovec>
    private let vmnetReadIovs: UnsafeMutablePointer<iovec>
    private let vnetHdr: UnsafeMutablePointer<virtio_net_hdr>

    init(config: BridgeNetworkConfig) throws {
        self.config = config

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

        let (_ifRef, ifParam) = try vmnetStartInterface(ifDesc: ifDesc, queue: queue)
        self.ifRef = _ifRef
        //print("if param: \(ifParam)")
        let maxPacketSize = xpc_dictionary_get_uint64(ifParam, vmnet_max_packet_size_key)

        // pre-allocate buffers
        // theoretically: max 200 packets, but 65k*200 is big so limit it
        vmnetReadIovs = UnsafeMutablePointer<iovec>.allocate(capacity: maxPacketsPerRead)
        var pktDescs = [vmpktdesc]()
        pktDescs.reserveCapacity(maxPacketsPerRead)

        // update iov pointers
        for i in 0..<maxPacketsPerRead {
            // allocate buf and set in iov
            vmnetReadIovs[i].iov_base = UnsafeMutableRawPointer.allocate(byteCount: Int(maxPacketSize), alignment: 1)
            vmnetReadIovs[i].iov_len = Int(maxPacketSize)

            // set in pktDesc
            let pktDesc = vmpktdesc(vm_pkt_size: Int(maxPacketSize),
                vm_pkt_iov: vmnetReadIovs.advanced(by: i),
                vm_pkt_iovcnt: 1,
                vm_flags: 0)
            pktDescs.append(pktDesc)
        }

        // more buffers
        let tapFd = config.tapFd
        fdReadSource = DispatchSource.makeReadSource(fileDescriptor: tapFd, queue: queue)
        fdReadIovs = UnsafeMutablePointer<iovec>.allocate(capacity: 1)
        fdReadIovs[0].iov_base = UnsafeMutableRawPointer.allocate(byteCount: Int(maxPacketSize), alignment: 1)
        vnetHdr = UnsafeMutablePointer<virtio_net_hdr>.allocate(capacity: 1)

        // must keep self ref to prevent deinit while referenced
        let ret = vmnet_interface_set_event_callback(ifRef, .VMNET_INTERFACE_PACKETS_AVAILABLE, queue) { [self] (eventMask, event) in
            //let numPackets = xpc_dictionary_get_uint64(event, vmnet_estimated_packets_available_key)
            //print("num packets: \(numPackets)")

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
                let vnetHdrSize = MemoryLayout<virtio_net_hdr>.size
                guard pktDesc.vm_pkt_size + vnetHdrSize <= 65535 else {
                    //print("packet too big: \(pktDesc.vm_pkt_size + vnetHdrSize)")
                    continue
                }

                do {
                    vnetHdr[0] = try buildVnetHdr(pkt: pktDesc.vm_pkt_iov[0].iov_base, pktLen: pktDesc.vm_pkt_size)
                } catch {
                    NSLog("[brnet] error building hdr: \(error)")
                    continue
                }
                var iovs = [
                    iovec(iov_base: vnetHdr, iov_len: vnetHdrSize),
                    iovec(iov_base: pktDesc.vm_pkt_iov[0].iov_base, iov_len: pktDesc.vm_pkt_size)
                ]
                let totalSize = pktDesc.vm_pkt_size + vnetHdrSize
                //print("writing \(totalSize) bytes to tap")
                let ret = writev(tapFd, &iovs, 2)
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
                vmnetReadIovs[i].iov_base?.deallocate()
            }
            fdReadIovs[0].iov_base?.deallocate()
            fdReadIovs.deallocate()
            vmnetReadIovs.deallocate()
            vnetHdr.deallocate()

            throw VmnetError.from(ret)
        }

        // read from tap, write to vmnet
        fdReadSource.setEventHandler { [self] in
            while true {
                // read from
                let buf = fdReadIovs[0].iov_base!
                let n = read(tapFd, buf, Int(maxPacketSize))
                guard n > 0 else {
                    if errno != EAGAIN && errno != EWOULDBLOCK {
                        NSLog("[brnet] tap read error: \(errno)")
                    }
                    return
                }

                // set in iov
                fdReadIovs[0].iov_len = n

                // write to vmnet
                var pktDesc = vmpktdesc(
                    vm_pkt_size: n,
                    vm_pkt_iov: fdReadIovs,
                    vm_pkt_iovcnt: 1,
                    vm_flags: 0
                )
                var pktsWritten = Int32(1)
                let ret2 = vmnet_write(ifRef, &pktDesc, &pktsWritten)
                guard ret2 == .VMNET_SUCCESS else {
                    NSLog("[brnet] vmnet write error: \(VmnetError.from(ret2))")
                    return
                }
            }
        }
        fdReadSource.resume()
    }

    func close() {
        fdReadSource.cancel()
        fdReadSource.setEventHandler(handler: nil)

        let sem = DispatchSemaphore(value: 0)
        let ret = vmnet_stop_interface(ifRef, queue) { status in
            if status != .VMNET_SUCCESS {
                NSLog("[brnet] stop status: \(VmnetError.from(status))")
            }
            sem.signal()
        }
        guard ret == .VMNET_SUCCESS else {
            NSLog("[brnet] stop error: \(VmnetError.from(ret))")
            return
        }

        sem.wait()
    }

    deinit {
        // safe to deallocate now that refs from callbacks are gone
        for i in 0..<maxPacketsPerRead {
            vmnetReadIovs[i].iov_base.deallocate()
        }
        fdReadIovs[0].iov_base.deallocate()

        // must free after, so refs are valid
        fdReadIovs.deallocate()
        vmnetReadIovs.deallocate()
    }
}

@_cdecl("swext_brnet_create")
func swext_brnet_create(configJsonStr: UnsafePointer<CChar>) -> UnsafeMutablePointer<GovzfResultCreate> {
    let configJson = String(cString: configJsonStr)
    let config = try! JSONDecoder().decode(BridgeNetworkConfig.self, from: configJson.data(using: .utf8)!)

    let result = ResultWrapper<GovzfResultCreate>()
    do {
        do {
            let obj = try BridgeNetwork(config: config)
            // take a long-lived ref for Go
            let ptr = Unmanaged.passRetained(obj).toOpaque()
            result.set(GovzfResultCreate(ptr: ptr, err: nil, rosetta_canceled: false))
        } catch {
            let prettyError = "\(error)"
            result.set(GovzfResultCreate(ptr: nil, err: strdup(prettyError.cString(using: .utf8)!), rosetta_canceled: false))
        }
    }

    return result.waitPtr()
}

@_cdecl("swext_brnet_close")
func swext_brnet_close(ptr: UnsafeMutableRawPointer) {
    // ref is dropped at the end of this function
    let obj = Unmanaged<BridgeNetwork>.fromOpaque(ptr).takeRetainedValue()
    obj.close()
}