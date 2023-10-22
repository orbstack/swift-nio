//
// Created by Danny Lin on 5/22/23.
//

import Foundation
import AppKit
import CBridge
import Defaults

private let guiBundleId = "dev.kdrag0n.MacVirt"

struct UserSettings: Codable {
    let showMenubarExtra: Bool
}

private func getUserSettings() -> UserSettings {
    return UserSettings(
        // TODO better way to tell Go about GUI running
        showMenubarExtra: Defaults[.globalShowMenubarExtra] && !isGuiRunning()
    )
}

private func isGuiRunning() -> Bool {
    return NSRunningApplication.runningApplications(withBundleIdentifier: guiBundleId).count > 0
}

@_cdecl("swext_defaults_get_user_settings")
func swext_defaults_get_user_settings() -> UnsafeMutablePointer<CChar> {
    do {
        let settings = getUserSettings()
        let data = try JSONEncoder().encode(settings)
        let str = String(data: data, encoding: .utf8)!
        // go frees the copy
        return strdup(str)
    } catch {
        return strdup("E\(error)")
    }
}
