//
// Created by Danny Lin on 8/15/23.
//

import CBridge
import Defaults
import Foundation

private let client = PHClient()

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

    return doGenericErr {
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
    doGenericErr {
        try await client.uninstall()
    }
}
