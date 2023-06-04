//
//  Macvlan.swift
//  GoVZF
//
//  Created by Danny Lin on 6/2/23.
//

import Foundation
import CBridge

// separate queue to avoid deadlocks
private let routerQueue = DispatchQueue(label: "dev.kdrag0n.swext.router")

// a bit under macOS limit of 32
// we can theoretically get up to 128 (7 bits)
private let maxMacvlanInterfaces = 28
// we don't have vmnet packet size info yet here, so it's easier to just use the max possible size
private let maxPossiblePacketSize: UInt64 = 65536 + 14

// serialied by routerQueue barriers
class VlanRouter {
    // static circular array of slots
    private var interfaces = [BridgeNetwork?](repeating: nil, count: maxMacvlanInterfaces)
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
        // barrier for sync
        return try routerQueue.sync(flags: .barrier) {
            let index = try firstFreeInterfaceIndex()

            // update last octet of MAC
            // lower 7 bits = index
            // upper 1 bit = 0 (host)
            var config = config
            config.hostOverrideMac[5] = UInt8(index & 0x7f)

            let bridge = try BridgeNetwork(config: config)
            interfaces[Int(index)] = bridge
            return index
        }
    }

    func removeBridge(index: BrnetInterfaceIndex) throws {
        try routerQueue.sync(flags: .barrier) { [self] in
            let bridge = try interfaceAt(index: index)
            bridge.close()
            interfaces[Int(index)] = nil
        }
    }

    func renewBridge(index: BrnetInterfaceIndex) throws {
        try routerQueue.sync(flags: .barrier) { [self] in
            let bridge = try interfaceAt(index: index)
            bridge.close()
            interfaces[Int(index)] = try BridgeNetwork(config: bridge.config)
        }
    }

    func clearBridges() {
        routerQueue.sync(flags: .barrier) { [self] in
            for (i, bridge) in interfaces.enumerated() {
                if let bridge {
                    bridge.close()
                    interfaces[i] = nil
                }
            }
        }
    }

    private func interfaceAt(index: BrnetInterfaceIndex) throws -> BridgeNetwork {
        // bounds check
        if index >= interfaces.count {
            throw BrnetError.interfaceNotFound
        }

        guard let bridge = interfaces[Int(index)] else {
            throw BrnetError.interfaceNotFound
        }

        return bridge
    }

    private func firstFreeInterfaceIndex() throws -> BrnetInterfaceIndex {
        for (i, bridge) in interfaces.enumerated() {
            print("checking \(i) = \(bridge)")
            if bridge == nil {
                return BrnetInterfaceIndex(i)
            }
        }
        throw BrnetError.tooManyInterfaces
    }

    func close() {
        // clear all bridges
        clearBridges()
        // close guest reader (breaks ref cycle by dropping handler ref)
        guestReader.close()
    }
}

@_cdecl("swext_vlanrouter_new")
func swext_vlanrouter_new(guestFd: Int32) -> UnsafeMutableRawPointer {
    let router = VlanRouter(guestFd: guestFd)
    return Unmanaged.passRetained(router).toOpaque()
}

@_cdecl("swext_vlanrouter_addBridge")
func swext_vlanrouter_addBridge(ptr: UnsafeMutableRawPointer, configJsonStr: UnsafePointer<CChar>) -> GResultIntErr {
    let config: BridgeNetworkConfig = decodeJson(configJsonStr)
    return doGenericErrInt(ptr) { (router: VlanRouter) in
        return Int64(try router.addBridge(config: config))
    }
}

@_cdecl("swext_vlanrouter_removeBridge")
func swext_vlanrouter_removeBridge(ptr: UnsafeMutableRawPointer, index: BrnetInterfaceIndex) -> GResultErr {
    doGenericErr(ptr) { (router: VlanRouter) in
        try router.removeBridge(index: index)
    }
}

@_cdecl("swext_vlanrouter_renewBridge")
func swext_vlanrouter_renewBridge(ptr: UnsafeMutableRawPointer, index: BrnetInterfaceIndex) -> GResultErr {
    doGenericErr(ptr) { (router: VlanRouter) in
        try router.renewBridge(index: index)
    }
}

@_cdecl("swext_vlanrouter_clearBridges")
func swext_vlanrouter_clearBridges(ptr: UnsafeMutableRawPointer) {
    doGeneric(ptr) { (router: VlanRouter) in
        router.clearBridges()
    }
}

@_cdecl("swext_vlanrouter_close")
func swext_vlanrouter_close(ptr: UnsafeMutableRawPointer) {
    // ref is dropped at the end of this function
    let obj = Unmanaged<VlanRouter>.fromOpaque(ptr).takeRetainedValue()
    obj.close()
}
