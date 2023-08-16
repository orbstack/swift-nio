//
// Created by Danny Lin on 8/15/23.
//

import Foundation
import CBridge
import Defaults

private let client = PHClient()
private let dummyPtr = Unmanaged.passRetained(client).toOpaque()

private let maxAdminDismissCount = 2 // auto-disable

private enum PHFFIError: Error {
    case canceledAndReachedMaxDismissCount // name exposed to FFI
}

@_cdecl("swext_privhelper_set_install_reason")
func swext_privhelper_set_install_reason(reasonC: UnsafePointer<CChar>) {
    let reason = String(cString: reasonC)
    client.installReason = reason
}

@_cdecl("swext_privhelper_symlink")
func swext_privhelper_symlink(srcC: UnsafePointer<CChar>, destC: UnsafePointer<CChar>) -> GResultErr {
    let src = String(cString: srcC)
    let dest = String(cString: destC)
    NSLog("symlink: \(src) -> \(dest)")

    // need a dummy pointer to use async wrapper
    return doGenericErr(dummyPtr) { (_: PHClient) in
        do {
            try await client.symlink(src: src, dest: dest)
        } catch PHError.canceled {
            if Defaults[.adminDismissCount] >= maxAdminDismissCount {
                throw PHFFIError.canceledAndReachedMaxDismissCount
            } else {
                throw PHError.canceled
            }
        }
    }
}

@_cdecl("swext_privhelper_uninstall")
func swext_privhelper_uninstall() -> GResultErr {
    doGenericErr(dummyPtr) { (_: PHClient) in
        try await client.uninstall()
    }
}