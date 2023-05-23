//
// Created by Danny Lin on 5/22/23.
//

import Foundation
import AppKit

struct UserSettings: Codable {
    let showMenubarExtra: Bool
}

private func getUserSettings() -> UserSettings {
    // vmgr has different bundle id, depending on signing id
    let defaults: UserDefaults
    if Bundle.main.bundleIdentifier == "dev.kdrag0n.MacVirt" {
        defaults = UserDefaults.standard
    } else {
        defaults = UserDefaults(suiteName: "dev.kdrag0n.MacVirt")!
    }

    defaults.register(defaults: [
        "global_showMenubarExtra": true,
    ])

    return UserSettings(
        // TODO better way to tell Go about GUI running
        showMenubarExtra: defaults.bool(forKey: "global_showMenubarExtra") && !isGuiRunning()
    )
}

private func isGuiRunning() -> Bool {
    let bundleId = "dev.kdrag0n.MacVirt"
    let runningApps = NSWorkspace.shared.runningApplications
    let isRunning = !runningApps.filter { $0.bundleIdentifier == bundleId }.isEmpty
    return isRunning
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
