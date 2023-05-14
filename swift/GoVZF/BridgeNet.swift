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

class BridgeNetwork {
    private let tapFd: Int32
    private let ifRef: interface_ref
    private let fdReadSource: DispatchSourceRead
    private let fdReadIovs: UnsafeMutablePointer<iovec>
    private let vmnetReadIovs: UnsafeMutablePointer<iovec>

    init(tapFd: Int32) throws {
        let ifDesc = xpc_dictionary_create(nil, nil, 0)
        xpc_dictionary_set_uint64(ifDesc, vmnet_operation_mode_key, UInt64(operating_modes_t.VMNET_HOST_MODE.rawValue))
        // macOS max MTU = 16384, but we use TSO instead
        xpc_dictionary_set_uint64(ifDesc, vmnet_mtu_key, 1500)

        // UUID
        var uuidBytes = [UInt8](repeating: 0, count: 16)
        (bridgeUuid as NSUUID).getBytes(&uuidBytes)
        xpc_dictionary_set_uuid(ifDesc, vmnet_interface_id_key, uuidBytes)

        xpc_dictionary_set_uuid(ifDesc, vmnet_network_identifier_key, uuidBytes)
        xpc_dictionary_set_string(ifDesc, vmnet_host_ip_address_key, "100.115.93.3")
        xpc_dictionary_set_string(ifDesc, vmnet_host_subnet_mask_key, "255.255.255.0")
        xpc_dictionary_set_string(ifDesc, vmnet_host_ipv6_address_key, "fd00:96dc:7096:1d00::3")
        /* vmnet_start_address_key, vmnet_end_address_key, vmnet_subnet_mask_key are for shared/NAT */

        // use our own MAC address (allow any)
        xpc_dictionary_set_bool(ifDesc, vmnet_allocate_mac_address_key, false)

        // TSO and checksum offload
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_checksum_offload_key, true)
        // TODO: can't use this on macOS 12
        xpc_dictionary_set_bool(ifDesc, vmnet_enable_tso_key, true)

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

        let vnetHdr = UnsafeMutablePointer<virtio_net_hdr>.allocate(capacity: 1)
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
                do {
                    vnetHdr[0] = try buildVnetHdr(pkt: pktDesc.vm_pkt_iov[0].iov_base, pktLen: pktDesc.vm_pkt_size)
                } catch {
                    print("error building vnet hdr: \(error)")
                    continue
                }
                var iovs = [
                    iovec(iov_base: vnetHdr, iov_len: MemoryLayout<virtio_net_hdr>.size),
                    iovec(iov_base: pktDesc.vm_pkt_iov[0].iov_base, iov_len: pktDesc.vm_pkt_size)
                ]
                let totalSize = pktDesc.vm_pkt_size + MemoryLayout<virtio_net_hdr>.size
                //print("writing \(totalSize) bytes to tap")
                let ret = writev(tapFd, &iovs, 2)
                guard ret == totalSize else {
                    print("write error: \(errno)")
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
            throw VmnetError.from(ret)
        }

        // read from tap, write to vmnet
        let tapSource = DispatchSource.makeReadSource(fileDescriptor: tapFd, queue: queue)
        let writeIov = UnsafeMutablePointer<iovec>.allocate(capacity: 1)
        writeIov[0].iov_base = malloc(Int(maxPacketSize))
        tapSource.setEventHandler {
            while true {
                // read from
                let buf = writeIov[0].iov_base!
                let n = read(tapFd, buf, Int(maxPacketSize))
                guard n > 0 else {
                    if errno != EAGAIN {
                        print("tap read error: \(errno)")
                    }
                    return
                }

                // set in iov
                writeIov[0].iov_len = n

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

    deinit {
        fdReadSource.cancel()
        fdReadIovs.deallocate()
        vmnetReadIovs.deallocate()
        Darwin.close(tapFd)
        let ret = vmnet_stop_interface(ifRef, queue) { status in
            print("stop status: \(status)")
        }
        guard ret == .VMNET_SUCCESS else {
            print("stop error: \(VmnetError.from(ret))")
            return
        }
    }
}
