import CBridge

@_cdecl("swext_network_write_packet")
func swext_network_write_packet(handle: UnsafeMutableRawPointer, iovs: UnsafePointer<iovec>, numIovs: Int, totalLen: Int) -> Int32 {
    return -ENOSYS
}
