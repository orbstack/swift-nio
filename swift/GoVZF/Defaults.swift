//
// Created by Danny Lin on 5/22/23.
//

import Foundation

struct UserSettings: Codable {
    let showMenubarExtra: Bool
}

private func getUserSettings() -> UserSettings {
    // vmgr has different bundle id
    let defaults = UserDefaults(suiteName: "dev.kdrag0n.MacVirt")!
    defaults.register(defaults: [
        "global_showMenubarExtra": true,
    ])

    return UserSettings(
        showMenubarExtra: defaults.bool(forKey: "global_showMenubarExtra")
    )
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
