//
//  BridgeNet.swift
//  GoVZF
//
//  Created by Danny Lin on 5/13/23.
//

import CBridge
import Foundation
import vmnet

// serial queue because we only have one set of iovecs
// vague user-facing thread/queue name
let vmnetPktQueue = DispatchQueue(label: "dev.orbstack.brnet.1")
// avoid stop barrier deadlock by using a separate queue
// also use serial queue to be safe in case vmnet isn't thread safe
let vmnetControlQueue = DispatchQueue(label: "dev.orbstack.brnet.2")

private let maxPacketsPerRead = 64

// sometimes hangs for unknown reasons
// short timeout because vmnet_start_interface already returned; just hasn't run block yet
private let vmnetControlTimeout: TimeInterval = 8 // sec

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
    case startTimeout
    case stopTimeout

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

    func toErrno() -> Int32 {
        switch self {
        case .generalFailure:
            return EIO
        case .memFailure:
            return ENOMEM
        case .invalidArgument:
            return EINVAL
        case .setupIncomplete:
            return ENODEV
        case .invalidAccess:
            return EACCES
        case .packetTooBig:
            return EMSGSIZE
        case .bufferExhausted:
            return ENOBUFS
        case .tooManyPackets:
            return ERANGE
        case .sharingServiceBusy:
            return EBUSY
        case .noInterfaceRef:
            return EBADF
        case .startTimeout:
            return ETIMEDOUT
        case .stopTimeout:
            return ETIMEDOUT
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

enum GuestWriteError: Error {
    case bufferFull
    case backendDied
    case shortWrite
    case errno(Int32)
}

private func vmnetStartInterface(ifDesc: xpc_object_t, queue: DispatchQueue) throws -> (interface_ref, xpc_object_t) {
    let sem = DispatchSemaphore(value: 0)
    var outIfParam: xpc_object_t?
    var outStatus: vmnet_return_t = .VMNET_FAILURE

    let interfaceRef = vmnet_start_interface(ifDesc, queue) { status, ifParam in
        outStatus = status
        outIfParam = ifParam
        sem.signal()
    }

    guard let interfaceRef else {
        throw VmnetError.noInterfaceRef
    }

    guard sem.wait(timeout: .now() + vmnetControlTimeout) == .success else {
        vmnet_stop_interface(interfaceRef, queue) { _ in }
        throw VmnetError.startTimeout
    }
    guard outStatus == .VMNET_SUCCESS, let outIfParam else {
        throw VmnetError.from(outStatus)
    }

    return (interfaceRef, outIfParam)
}

struct BridgeNetworkConfig: Codable {
    let guestHandle: NetHandle
    let guestSconHandle: NetHandle
    let ownsGuestReader: Bool

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

class BridgeNetwork: NetCallbacks {
    let config: BridgeNetworkConfig

    private let ifRef: interface_ref
    private let processor: PacketProcessor
    private var guestReader: GuestReader?
    private let hostReadIovs: UnsafeMutablePointer<iovec>

    private var closed = false

    init(config: BridgeNetworkConfig) throws {
        self.config = config
        processor = PacketProcessor(hostOverrideMac: config.hostOverrideMac,
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
        // do not set IFBIF_PRIVATE. allow packet forwarding across bridges so Parallels Windows VM can connect
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_isolation_key, false)

        // TSO and checksum offload
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_checksum_offload_key, true)
        // enable TSO if link MTU allows
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_tso_key, config.maxLinkMtu >= 65535)

        let (_ifRef, ifParam) = try vmnetStartInterface(ifDesc: ifDesc, queue: vmnetControlQueue)
        ifRef = _ifRef
        // print("if param: \(ifParam)")
        let maxPacketSize = xpc_dictionary_get_uint64(ifParam, vmnet_max_packet_size_key)
        // vnet header only if mtu = max
        let needsVnetHdr = maxPacketSize >= 65535

        // pre-allocate buffers
        // theoretically: max 200 packets, but 65k*200 is big so limit it
        hostReadIovs = UnsafeMutablePointer<iovec>.allocate(capacity: maxPacketsPerRead)
        var pktDescs = [vmpktdesc]()
        pktDescs.reserveCapacity(maxPacketsPerRead)

        // update iov pointers
        for i in 0 ..< maxPacketsPerRead {
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

        // must keep self ref to prevent deinit while referenced
        let ret = vmnet_interface_set_event_callback(ifRef, .VMNET_INTERFACE_PACKETS_AVAILABLE, vmnetPktQueue) { [self] _, _ in
            // print("num packets: \(xpc_dictionary_get_uint64(event, vmnet_estimated_packets_available_key))")

            // read as many packets as we can
            var pktsRead = Int32(maxPacketsPerRead) // max
            let ret = vmnet_read(ifRef, &pktDescs, &pktsRead)
            guard ret == .VMNET_SUCCESS else {
                NSLog("[brnet] read error: \(VmnetError.from(ret))")
                return
            }

            // send packets to tap
            for i in 0 ..< Int(pktsRead) {
                let pktDesc = pktDescs[i]

                // sanity: never write a packet > 65535 bytes. that breaks the network, so just drop it
                let vnetHdrSize = needsVnetHdr ? MemoryLayout<virtio_net_hdr_v1>.size : 0
                guard pktDesc.vm_pkt_size + vnetHdrSize <= 65535 else {
                    // print("packet too big: \(pktDesc.vm_pkt_size + vnetHdrSize)")
                    continue
                }

                var guestHandle = config.guestHandle
                let pkt = Packet(desc: pktDesc)
                var vnetHdr: virtio_net_hdr_v1
                do {
                    let redirectToScon = try processor.processToGuest(pkt: pkt)
                    if redirectToScon {
                        guestHandle = config.guestSconHandle
                    }
                    vnetHdr = try processor.buildVnetHdr(pkt: pkt)
                } catch {
                    switch error {
                    case BrnetError.dropPacket:
                        break
                    case BrnetError.redirectToHost:
                        // redirect to host for NDP responder
                        var iov = iovec(iov_base: pkt.data, iov_len: pkt.accessibleLen)
                        _ = writePacket(iovs: &iov, numIovs: 1, len: pkt.accessibleLen)
                        continue
                    default:
                        NSLog("[brnet] error processing/building hdr: \(error)")
                    }
                    continue
                }
                
                let totalLen = pkt.totalLen + vnetHdrSize
                do {
                    try withUnsafeMutableBytes(of: &vnetHdr) { vnetHdrPtr in
                        var iovs = two_iovecs(iovs: (
                            iovec(iov_base: vnetHdrPtr.baseAddress, iov_len: vnetHdrSize),
                            iovec(iov_base: pkt.data, iov_len: pkt.accessibleLen)
                        ))
                        // we only create 1-iovec packet buffers here
                        assert(pkt.accessibleLen == pkt.totalLen)
                        try withUnsafeMutablePointer(to: &iovs.iovs) { iovsPtr in
                            try iovsPtr.withMemoryRebound(to: iovec.self, capacity: 2) { iovsPtr in
                                try self.writeToGuest(handle: guestHandle, iovs: iovsPtr, numIovs: 2, totalLen: totalLen)
                            }
                        }
                    }
                } catch {
                    switch error {
                    case GuestWriteError.bufferFull:
                        // socket is full. drop the packet
                        continue
                    case GuestWriteError.backendDied:
                        // VMM stopped and closed the other side of the datagram socketpair
                        // avoid trying to unset the event handler ourselves -- high risk of deadlock
                        // Go should stop and close the BridgeNetwork soon
                        // don't try to send remaining packets
                        break
                    default:
                        NSLog("[brnet] write error: \(error)")
                    }
                }
            }

            // reset descs
            for i in 0 ..< Int(pktsRead) {
                pktDescs[i].vm_pkt_size = Int(maxPacketSize)
                pktDescs[i].vm_pkt_iovcnt = 1
                pktDescs[i].vm_flags = 0
            }
        }
        guard ret == .VMNET_SUCCESS else {
            // dealloc
            for i in 0 ..< maxPacketsPerRead {
                hostReadIovs[i].iov_base?.deallocate()
            }
            hostReadIovs.deallocate()

            throw VmnetError.from(ret)
        }

        // read from guest, write to vmnet
        if config.ownsGuestReader {
            // if we own the interface and read from it, register as the Rust network interface callback
            switch config.guestHandle {
            case .rsvm:
                NetworkHandles.setCallbacks(index: NetworkHandles.handleSconBridge, cb: self)
            case .fd(let fd):
                guestReader = GuestReader(guestFd: fd, maxPacketSize: maxPacketSize,
                                        onPacket: { [self] iovs, numIovs, len in
                                            _ = writePacket(iovs: iovs, numIovs: numIovs, len: len)
                                        })
            }
        }
    }

    func writePacket(iovs: UnsafePointer<iovec>, numIovs: Int, len: Int) -> Int32 {
        // process packet
        let pkt = Packet(iovs: iovs, len: len)
        let opts: PacketWriteOptions
        do {
            opts = try processor.processToHost(pkt: pkt)
        } catch {
            NSLog("[brnet] error processing to host: \(error)")
            return -EINVAL
        }

        // write to vmnet
        var pktDesc = vmpktdesc(vm_pkt_size: len,
                                // shouldn't be written
                                vm_pkt_iov: UnsafeMutablePointer(mutating: iovs),
                                vm_pkt_iovcnt: UInt32(numIovs),
                                vm_flags: 0)
        var pktsWritten: Int32 = 1
        let ret2 = vmnet_write(ifRef, &pktDesc, &pktsWritten)
        guard ret2 == .VMNET_SUCCESS else {
            NSLog("[brnet] host write error: \(VmnetError.from(ret2))")
            return VmnetError.from(ret2).toErrno()
        }

        // need to write again for TCP ECN SYN->RST workaround?
        if opts.sendDuplicate {
            let ret2 = vmnet_write(ifRef, &pktDesc, &pktsWritten)
            guard ret2 == .VMNET_SUCCESS else {
                NSLog("[brnet] host write error: \(VmnetError.from(ret2))")
                return VmnetError.from(ret2).toErrno()
            }
        }

        return 0
    }

    func writeToGuest(handle: NetHandle, iovs: UnsafePointer<iovec>, numIovs: Int, totalLen: Int) throws {
        let ret = switch handle {
        case .rsvm(let handle):
            rsvm_network_write_packet(handle, iovs, numIovs, totalLen)
        case .fd(let fd):
            writev(fd, iovs, Int32(numIovs)) == -1 ? -errno : 0
        }

        guard ret >= 0 else {
            switch ret {
            case -EAGAIN, -EWOULDBLOCK, -ENOBUFS:
                throw GuestWriteError.bufferFull
            case -EPIPE, -ECONNRESET, -EDESTADDRREQ:
                throw GuestWriteError.backendDied
            default:
                throw GuestWriteError.errno(errno)
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
        ret = vmnetPktQueue.sync(flags: .barrier) {
            // sem still needed to wait for vmnet_stop_interface completion
            vmnet_stop_interface(ifRef, vmnetControlQueue) { status in
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
        guard sem.wait(timeout: .now() + vmnetControlTimeout) == .success else {
            NSLog("[brnet] stop timeout")
            return
        }
    }

    deinit {
        // guestReader will get deinited too
        // safe to deallocate now that refs from callbacks are gone
        for i in 0 ..< maxPacketsPerRead {
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
        return GResultCreate(ptr: ptr, err: nil)
    } catch {
        let prettyError = "\(error)"
        return GResultCreate(ptr: nil, err: strdup(prettyError))
    }
}

@_cdecl("swext_brnet_close")
func swext_brnet_close(ptr: UnsafeMutableRawPointer) {
    // ref is dropped at the end of this function
    let obj = Unmanaged<BridgeNetwork>.fromOpaque(ptr).takeRetainedValue()
    obj.close()
}
