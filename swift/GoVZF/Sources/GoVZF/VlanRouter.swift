//
//  VlanRouter.swift
//  GoVZF
//
//  Created by Danny Lin on 6/2/23.
//

import CBridge
import Foundation
import Network

private let verboseDebug = false

// separate queue to avoid deadlocks
private let routerQueue = DispatchQueue(label: "dev.orbstack.swext.router")

// we don't have vmnet packet size info yet here, so it's easier to just use the max possible size
private let maxPossiblePacketSize: UInt64 = 65536 + 14

struct VlanRouterConfig: Codable {
    let guestHandle: NetHandle
    let macPrefix: [UInt8]
    let maxVlanInterfaces: Int
}

// serialied by routerQueue barriers
// host->guest = macvlan, filtered by host source MAC on Linux side
// guest->host = destination MAC or broadcast, because src MAC will be containers or Docker bridge
class VlanRouter: NetCallbacks {
    // static circular array of slots
    private var interfaces: [BridgeNetwork?]
    private var guestReader: GuestReader?
    private let pathMonitor = NWPathMonitor()

    private let macPrefix: [UInt8]

    init(config: VlanRouterConfig) {
        macPrefix = config.macPrefix

        interfaces = [BridgeNetwork?](repeating: nil, count: config.maxVlanInterfaces)

        // we only *read* packets from the guest, and dispatch them to BridgeNetworks
        // each BridgeNetwork writes directly to the guest, as it has the right MAC
        switch config.guestHandle {
        case .rsvm:
            // if we're using Rust handles + callbacks, don't start a reader;
            // Rust will write all packets to the router directly
            // just register the handle for that
            NetworkHandles.setCallbacks(index: NetworkHandles.handleVlanRouter, cb: self)
        case .fd(let fd):
            guestReader = GuestReader(
                guestFd: fd, maxPacketSize: maxPossiblePacketSize,
                onPacket: { [self] iovs, numIovs, len in
                    let _ = writePacket(iovs: iovs, numIovs: numIovs, len: len)
                })
        }

        // monitor route for renewal
        // more reliable per-NWConnection UDP pathUpdateHandler, which is more granular:
        // self-feedback loop is impossible because vmnet doesn't trigger a NWPathMonitor change
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

    func writePacket(iovs: UnsafePointer<iovec>, numIovs: Int, len: Int) -> Int32 {
        let pkt = Packet(iovs: iovs, len: len)
        do {
            let ifi = try PacketProcessor.extractInterfaceIndexToHost(
                pkt: pkt, macPrefix: self.macPrefix)
            if ifi == ifiBroadcast {
                // broadcast to all interfaces
                for bridge in interfaces {
                    if let bridge {
                        let err = bridge.writePacket(iovs: iovs, numIovs: numIovs, len: len)
                        guard err == 0 else {
                            return err
                        }
                    }
                }
                return 0
            } else {
                // unicast
                let bridge = try interfaceAt(index: ifi)
                return bridge.writePacket(iovs: iovs, numIovs: numIovs, len: len)
            }
        } catch {
            switch error {
            case BrnetError.interfaceNotFound:
                // normal that some packets get dropped for no vlan match
                return 0
            default:
                NSLog("[brnet/router] invalid MAC or routing info: \(error)")
                return -EINVAL
            }
        }
    }

    func addBridge(config: BridgeNetworkConfig) throws -> BrnetInterfaceIndex {
        // barrier for sync
        return try routerQueue.sync(flags: .barrier) {
            let index = try firstFreeInterfaceIndex()

            // update last octet of MAC
            // lower 7 bits = index
            // upper 1 bit = 0 (host)
            var config = config
            config.hostOverrideMac[5] = UInt8(index & 0x7F)
            config.guestMac[5] = UInt8((index & 0x7F) | 0x80)

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
        guestReader?.close()
    }
}

@_cdecl("swext_vlanrouter_new")
func swext_vlanrouter_new(configJsonStr: UnsafePointer<CChar>) -> UnsafeMutableRawPointer {
    let config: VlanRouterConfig = decodeJson(configJsonStr)
    let router = VlanRouter(config: config)
    return Unmanaged.passRetained(router).toOpaque()
}

@_cdecl("swext_vlanrouter_addBridge")
func swext_vlanrouter_addBridge(ptr: UnsafeMutableRawPointer, configJsonStr: UnsafePointer<CChar>)
    -> GResultIntErr
{
    let config: BridgeNetworkConfig = decodeJson(configJsonStr)
    return doGenericErrInt(ptr) { (router: VlanRouter) in
        try Int64(router.addBridge(config: config))
    }
}

@_cdecl("swext_vlanrouter_removeBridge")
func swext_vlanrouter_removeBridge(ptr: UnsafeMutableRawPointer, index: BrnetInterfaceIndex)
    -> GResultErr
{
    doGenericErr(ptr) { (router: VlanRouter) in
        try router.removeBridge(index: index)
    }
}

@_cdecl("swext_vlanrouter_renewBridge")
func swext_vlanrouter_renewBridge(ptr: UnsafeMutableRawPointer, index: BrnetInterfaceIndex)
    -> GResultErr
{
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
