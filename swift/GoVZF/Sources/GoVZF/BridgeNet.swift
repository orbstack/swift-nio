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
let vmnetQueue = DispatchQueue(label: "dev.orbstack.swext.bridge", attributes: .concurrent)

private let dgramSockBuf = 512 * 1024
private let maxPacketsPerRead = 64

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

enum BrnetError: Error {
    case errno(Int32)
    case invalidPacket

    case interfaceNotFound
    case tooManyInterfaces

    case dropPacket
    case redirectToHost
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

struct BridgeNetworkConfig: Codable {
    let guestFd: Int32
    let shouldReadGuest: Bool

    let uuid: String
    let ip4Address: String?
    let ip4Mask: String
    // always /64
    let ip6Address: String?

    var hostOverrideMac: [UInt8] // for vlans: template, filled in by addBridge
    var guestMac: [UInt8] // for vlans: template, filled in by addBridge
    let ndpReplyPrefix: [UInt8]?
    let allowMulticast: Bool

    // 65535 on macOS 12+
    let maxLinkMtu: Int
}

class BridgeNetwork {
    let config: BridgeNetworkConfig

    private let ifRef: interface_ref
    private let processor: PacketProcessor
    private var guestReader: GuestReader? = nil
    private let hostReadIovs: UnsafeMutablePointer<iovec>
    private let vnetHdr: UnsafeMutablePointer<virtio_net_hdr>

    private var closed = false

    init(config: BridgeNetworkConfig) throws {
        self.config = config
        self.processor = PacketProcessor(hostOverrideMac: config.hostOverrideMac,
                allowMulticast: config.allowMulticast,
                ndpReplyPrefix: config.ndpReplyPrefix,
                guestMac: config.guestMac)

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
        if let ip4Address = config.ip4Address {
            xpc_dictionary_set_string(ifDesc, vmnet_host_ip_address_key, ip4Address)
            xpc_dictionary_set_string(ifDesc, vmnet_host_subnet_mask_key, config.ip4Mask)
        }
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
                    switch error {
                    case BrnetError.dropPacket:
                        break
                    case BrnetError.redirectToHost:
                        // redirect to host for NDP responder
                        var iov = iovec(iov_base: pkt.data, iov_len: pkt.len)
                        tryWriteToHost(iov: &iov, len: pkt.len)
                        continue
                    default:
                        NSLog("[brnet] error processing/building hdr: \(error)")
                    }
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
                        tryWriteToHost(iov: iov, len: len)
                    })
        }
    }

    func tryWriteToHost(iov: UnsafeMutablePointer<iovec>, len: Int) {
        // process packet
        let pkt = Packet(iov: iov, len: len)
        let opts: PacketWriteOptions
        do {
            opts = try processor.processToHost(pkt: pkt)
        } catch {
            NSLog("[brnet] error processing to host: \(error)")
            return
        }

        // write to vmnet
        var pktDesc = vmpktdesc(vm_pkt_size: len,
                vm_pkt_iov: iov,
                vm_pkt_iovcnt: 1,
                vm_flags: 0)
        var pktsWritten: Int32 = 1
        let ret2 = vmnet_write(ifRef, &pktDesc, &pktsWritten)
        guard ret2 == .VMNET_SUCCESS else {
            NSLog("[brnet] host write error: \(VmnetError.from(ret2))")
            return
        }

        // need to write again for TCP ECN SYN->RST workaround?
        if opts.sendDuplicate {
            let ret2 = vmnet_write(ifRef, &pktDesc, &pktsWritten)
            guard ret2 == .VMNET_SUCCESS else {
                NSLog("[brnet] host write error: \(VmnetError.from(ret2))")
                return
            }
        }
    }

    func close() {
        // don't allow double close to prevent segfault
        if closed {
            return
        }
        defer {
            closed = true
        }

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
func swext_brnet_create(configJsonStr: UnsafePointer<CChar>) -> GResultCreate {
    let config: BridgeNetworkConfig = decodeJson(configJsonStr)
    do {
        let obj = try BridgeNetwork(config: config)
        // take a long-lived ref for Go
        let ptr = Unmanaged.passRetained(obj).toOpaque()
        return GResultCreate(ptr: ptr, err: nil, rosetta_canceled: false)
    } catch {
        let prettyError = "\(error)"
        return GResultCreate(ptr: nil, err: strdup(prettyError), rosetta_canceled: false)
    }
}

@_cdecl("swext_brnet_close")
func swext_brnet_close(ptr: UnsafeMutableRawPointer) {
    // ref is dropped at the end of this function
    let obj = Unmanaged<BridgeNetwork>.fromOpaque(ptr).takeRetainedValue()
    obj.close()
}
