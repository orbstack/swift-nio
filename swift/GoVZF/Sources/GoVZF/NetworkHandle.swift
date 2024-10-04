import CBridge

// always reserve up to 3 handles (VZF network interface limit = 3)
private var networkHandles = [NetCallbacksRef](repeating: NetCallbacksRef(cb: nil), count: 3)

enum NetHandle: Codable {
    case rsvm(UInt)
    case fd(Int32)
}

private struct NetCallbacksRef {
    weak var cb: NetCallbacks?
}

protocol NetCallbacks: AnyObject {
    func writePacket(iovs: UnsafePointer<iovec>, numIovs: Int, len: Int) -> Int32
}

@_cdecl("swext_network_write_packet")
func swext_network_write_packet(
    handle: UnsafeMutableRawPointer, iovs: UnsafePointer<iovec>, numIovs: Int, totalLen: Int
) -> Int32 {
    let handleIdx = Int(bitPattern: handle)
    if handleIdx < 0 || handleIdx >= networkHandles.count {
        return -EBADF
    }

    let ref = networkHandles[handleIdx]
    guard let cb = ref.cb else {
        return -EPIPE
    }

    return cb.writePacket(iovs: iovs, numIovs: numIovs, len: totalLen)
}

enum NetworkHandles {
    static let handleSconBridge = 0
    static let handleVlanRouter = 1

    static func setCallbacks(index: Int, cb: NetCallbacks) {
        networkHandles[index] = NetCallbacksRef(cb: cb)
    }
}
