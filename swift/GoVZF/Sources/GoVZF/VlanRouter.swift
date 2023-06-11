//
//  Macvlan.swift
//  GoVZF
//
//  Created by Danny Lin on 6/2/23.
//

import Foundation
import CBridge
import Network

private let verboseDebug = false

// separate queue to avoid deadlocks
private let routerQueue = DispatchQueue(label: "dev.kdrag0n.swext.router")

// we don't have vmnet packet size info yet here, so it's easier to just use the max possible size
private let maxPossiblePacketSize: UInt64 = 65536 + 14

struct VlanRouterConfig: Codable {
    let guestFd: Int32
    let macPrefix: [UInt8]
    let maxVlanInterfaces: Int
}

// serialied by routerQueue barriers
// host->guest = macvlan, filtered by host source MAC on Linux side
// guest->host = destination MAC or broadcast, because src MAC will be containers or Docker bridge
class VlanRouter {
    // static circular array of slots
    private var interfaces: [BridgeNetwork?]
    private var guestReader: GuestReader! = nil
    private let pathMonitor = NWPathMonitor()

    init(config: VlanRouterConfig) {
        interfaces = [BridgeNetwork?](repeating: nil, count: config.maxVlanInterfaces)
        guestReader = GuestReader(guestFd: config.guestFd, maxPacketSize: maxPossiblePacketSize,
                onPacket: { [self] iov, len in
                    let pkt = Packet(iov: iov, len: len)
                    do {
                        let ifi = try PacketProcessor.extractInterfaceIndexToHost(pkt: pkt, macPrefix: config.macPrefix)
                        if ifi == ifiBroadcast {
                            // broadcast to all interfaces
                            for bridge in interfaces {
                                if let bridge {
                                    bridge.tryWriteToHost(iov: iov, len: len)
                                }
                            }
                        } else {
                            // unicast
                            let bridge = try interfaceAt(index: ifi)
                            bridge.tryWriteToHost(iov: iov, len: len)
                        }
                    } catch {
                        switch error {
                        case BrnetError.interfaceNotFound:
                            // normal that some packets get dropped for no vlan match
                            break
                        default:
                            NSLog("[brnet/router] failed to extract pkt routing info: \(error)")
                        }
                    }
                })

        pathMonitor.pathUpdateHandler = { path in
            if verboseDebug {
                print("NW path: \(path)")
                print("  status: \(path.status)")
                print("  availableInterfaces: \(path.availableInterfaces)")
                print("  gateways: \(path.gateways)")
            }

            swext_net_cb_path_changed()
        }
        pathMonitor.start(queue: routerQueue)
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
            if bridge == nil {
                return BrnetInterfaceIndex(i)
            }
        }
        throw BrnetError.tooManyInterfaces
    }

    func close() {
        pathMonitor.cancel()
        // clear all bridges
        clearBridges()
        // close guest reader (breaks ref cycle by dropping handler ref)
        guestReader.close()
    }
}

@_cdecl("swext_vlanrouter_new")
func swext_vlanrouter_new(configJsonStr: UnsafePointer<CChar>) -> UnsafeMutableRawPointer {
    let config: VlanRouterConfig = decodeJson(configJsonStr)
    let router = VlanRouter(config: config)
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
