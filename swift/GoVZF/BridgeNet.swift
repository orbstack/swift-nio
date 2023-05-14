//
//  BridgeNet.swift
//  GoVZF
//
//  Created by Danny Lin on 5/13/23.
//

import Foundation
import vmnet

private let queue = DispatchQueue(label: "dev.kdrag0n.govzf.bridge")
private let bridgeUuid = UUID(uuidString: "25ef1ee1-1ead-40fd-a97d-f9284917459b")!

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

private enum BridgeError: Error {
    case errno(Int32)
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

private func sys(_ ret: Int32) throws {
    guard ret != -1 else {
        throw BridgeError.errno(errno)
    }
}

private func setLargeBuffers(fd: Int32) throws {
    var size = UInt64(dgramSockBuf)
    try sys(setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &size, socklen_t(MemoryLayout<UInt64>.size)))
    size *= 4
    try sys(setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &size, socklen_t(MemoryLayout<UInt64>.size)))
}

private func newDatagramPair() throws -> (Int32, Int32) {
    var fds: [Int32] = [-1, -1]
    try sys(socketpair(AF_UNIX, SOCK_DGRAM, 0, &fds))

    // cloexec
    try sys(fcntl(fds[0], F_SETFD, FD_CLOEXEC))
    try sys(fcntl(fds[1], F_SETFD, FD_CLOEXEC))

    // set large buffers
    try setLargeBuffers(fd: fds[0])
    try setLargeBuffers(fd: fds[1])

    // nonblock
    try sys(fcntl(fds[0], F_SETFL, O_NONBLOCK))
    try sys(fcntl(fds[1], F_SETFL, O_NONBLOCK))

    return (fds[0], fds[1])
}

class BridgeNetwork {
    private let tapFd: Int32
    private let ifRef: interface_ref
    private let fdReadSource: DispatchSourceRead
    private let fdReadIovs: UnsafeMutablePointer<iovec>
    private let vmnetReadIovs: UnsafeMutablePointer<iovec>

    init(tapFd: Int32) throws {
        let ifDesc = xpc_dictionary_create(nil, nil, 0)
        xpc_dictionary_set_uint64(ifDesc, vmnet_operation_mode_key, UInt64(operating_modes_t.VMNET_HOST_MODE.rawValue))
        // macOS max MTU = 16384
        //xpc_dictionary_set_uint64(ifDesc, vmnet_mtu_key, 16384)
        xpc_dictionary_set_uint64(ifDesc, vmnet_mtu_key, 1500)

        // UUID
        var uuidBytes = [UInt8](repeating: 0, count: 16)
        (bridgeUuid as NSUUID).getBytes(&uuidBytes)
        xpc_dictionary_set_uuid(ifDesc, vmnet_interface_id_key, uuidBytes)

        xpc_dictionary_set_uuid(ifDesc, vmnet_network_identifier_key, uuidBytes)
        xpc_dictionary_set_string(ifDesc, vmnet_host_ip_address_key, "100.115.93.3")
        xpc_dictionary_set_string(ifDesc, vmnet_host_subnet_mask_key, "255.255.255.0")
        xpc_dictionary_set_string(ifDesc, vmnet_host_ipv6_address_key, "fd00:30:31::3")
        /* vmnet_start_address_key, vmnet_end_address_key, vmnet_subnet_mask_key are for shared/NAT */

        // use our own MAC address (allow any)
        xpc_dictionary_set_bool(ifDesc, vmnet_allocate_mac_address_key, false)

        // TSO and checksum offload
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_checksum_offload_key, true)
        //xpc_dictionary_set_bool(ifDesc, vmnet_enable_tso_key, false)

        let (ifRef, ifParam) = try vmnetStartInterface(ifDesc: ifDesc, queue: queue)
        print("if param: \(ifParam)")
        let maxPacketSize = xpc_dictionary_get_uint64(ifParam, vmnet_max_packet_size_key)

        // pre-allocate buffers
        // theoretically: max 200 packets, but 65k*200 is big so limit it
        let pktIovs = UnsafeMutablePointer<iovec>.allocate(capacity: maxPacketsPerRead)
        var pktDescs = [vmpktdesc]()
        pktDescs.reserveCapacity(maxPacketsPerRead)

        // update iov pointers
        for i in 0..<maxPacketsPerRead {
            // allocate buf and set in iov
            pktIovs[i].iov_base = malloc(Int(maxPacketSize))
            pktIovs[i].iov_len = Int(maxPacketSize)

            // set in pktDesc
            let pktDesc = vmpktdesc(
                    vm_pkt_size: Int(maxPacketSize),
                    vm_pkt_iov: pktIovs.advanced(by: i),
                    vm_pkt_iovcnt: 1,
                    vm_flags: 0
            )
            pktDescs.append(pktDesc)
        }

        let ret = vmnet_interface_set_event_callback(ifRef, .VMNET_INTERFACE_PACKETS_AVAILABLE, queue) { (eventMask, event) in
            //let numPackets = xpc_dictionary_get_uint64(event, vmnet_estimated_packets_available_key)
            //print("num packets: \(numPackets)")

            // read as many packets as we can
            var pktsRead = Int32(maxPacketsPerRead) // max
            let ret = vmnet_read(ifRef, &pktDescs, &pktsRead)
            guard ret == .VMNET_SUCCESS else {
                print("read error: \(VmnetError.from(ret))")
                return
            }

            // send packets to tap
            for i in 0..<Int(pktsRead) {
                let pktDesc = pktDescs[i]
                let pktIov = pktDesc.vm_pkt_iov.pointee
                let ret = write(tapFd, pktIov.iov_base, pktIov.iov_len)
                guard ret == pktIov.iov_len else {
                    print("write error: \(errno)")
                    continue
                }
            }

            // reset descs
            for i in 0..<Int(pktsRead) {
                pktDescs[i].vm_pkt_size = Int(maxPacketSize)
                pktDescs[i].vm_pkt_iovcnt = 1
                pktDescs[i].vm_flags = 0
                pktDescs[i].vm_pkt_iov.pointee.iov_len = Int(maxPacketSize)
            }
        }
        guard ret == .VMNET_SUCCESS else {
            throw VmnetError.from(ret)
        }

        // read from tap, write to vmnet
        let tapSource = DispatchSource.makeReadSource(fileDescriptor: tapFd, queue: queue)
        let writeIov = UnsafeMutablePointer<iovec>.allocate(capacity: 1)
        writeIov.pointee.iov_base = malloc(Int(maxPacketSize))
        tapSource.setEventHandler {
            while true {
                // read from
                let buf = writeIov.pointee.iov_base!
                let n = read(tapFd, buf, Int(maxPacketSize))
                guard n > 0 else {
                    if errno != EAGAIN {
                        print("tap read error: \(errno)")
                    }
                    return
                }

                // set in iov
                writeIov.pointee.iov_len = n

                // write to vmnet
                var pktDesc = vmpktdesc(
                        vm_pkt_size: n,
                        vm_pkt_iov: writeIov,
                        vm_pkt_iovcnt: 1,
                        vm_flags: 0
                )
                var pktsWritten = Int32(1)
                let ret2 = vmnet_write(ifRef, &pktDesc, &pktsWritten)
                guard ret2 == .VMNET_SUCCESS else {
                    print("vmnet write error: \(VmnetError.from(ret2))")
                    return
                }
            }
        }
        tapSource.resume()

        self.tapFd = tapFd
        self.ifRef = ifRef
        self.fdReadSource = tapSource
        self.fdReadIovs = writeIov
        self.vmnetReadIovs = pktIovs
    }

    static func newPair() throws -> (BridgeNetwork, Int32) {
        let (fd0, fd1) = try newDatagramPair()
        let bridgeNet = try BridgeNetwork(tapFd: fd0)
        return (bridgeNet, fd1)
    }
}
