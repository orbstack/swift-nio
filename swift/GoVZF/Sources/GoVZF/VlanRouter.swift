//
//  Macvlan.swift
//  GoVZF
//
//  Created by Danny Lin on 6/2/23.
//

import Foundation

// a bit under macOS limit of 32
// we can theoretically get up to 128 (7 bits)
private let maxMacvlanInterfaces = 28
// we don't have vmnet packet size info yet here, so it's easier to just use the max possible size
private let maxPossiblePacketSize: UInt64 = 65536 + 14

// serialied by vmnetQueue barriers
class VlanRouter {
    private var interfaces = [BridgeNetwork]()
    private var guestReader: GuestReader! = nil

    init(guestFd: Int32) {
        guestReader = GuestReader(guestFd: guestFd, maxPacketSize: maxPossiblePacketSize,
                onPacket: { [self] iov, len in
                    let pkt = Packet(iov: iov, len: len)
                    do {
                        let ifi = try PacketProcessor.extractInterfaceIndexToHost(pkt: pkt)
                        let bridge = try interfaceAt(index: ifi)
                        bridge.tryWriteToHost(iov: iov, len: len)
                    } catch {
                        NSLog("[brnet/router] failed to extract pkt routing info: \(error)")
                    }
                })
    }

    func addBridge(config: BridgeNetworkConfig) throws -> BrnetInterfaceIndex {
        let bridge = try BridgeNetwork(config: config)
        // barrier for sync
        return try vmnetQueue.sync(flags: .barrier) {
            if interfaces.count >= maxMacvlanInterfaces {
                throw BrnetError.tooManyInterfaces
            }

            interfaces.append(bridge)
            return BrnetInterfaceIndex(interfaces.count - 1)
        }
    }

    func removeBridge(index: BrnetInterfaceIndex) throws {
        try vmnetQueue.sync(flags: .barrier) { [self] in
            let bridge = try interfaceAt(index: index)
            bridge.close()
            interfaces.remove(at: Int(index))
        }
    }

    func renewBridge(index: BrnetInterfaceIndex) throws {
        try vmnetQueue.sync(flags: .barrier) { [self] in
            let bridge = try interfaceAt(index: index)
            bridge.close()
            let config = bridge.config
            interfaces[Int(index)] = try BridgeNetwork(config: config)
        }
    }

    func clearBridges() {
        // barrier for sync
        vmnetQueue.sync(flags: .barrier) { [self] in
            for bridge in interfaces {
                bridge.close()
            }
            interfaces.removeAll()
        }
    }

    private func interfaceAt(index: BrnetInterfaceIndex) throws -> BridgeNetwork {
        // bounds check
        if index >= interfaces.count {
            throw BrnetError.invalidInterface
        }

        return interfaces[Int(index)]
    }

    func close() {
        // clear all bridges
        clearBridges()
        // close guest reader (breaks ref cycle)
        guestReader.close()
    }
}
