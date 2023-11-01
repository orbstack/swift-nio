//
// Created by Danny Lin on 6/22/23.
//

import AppKit
import Foundation
import CBridge

private let firefoxRecentPeriod: TimeInterval = 3 * 30 * 24 * 60 * 60
private let firefoxBundleIds = [
    // stable / beta
    "org.mozilla.firefox",
    // nightly
    "org.mozilla.nightly",
    // developer edition
    "org.mozilla.firefoxdeveloperedition",
]

@_cdecl("swext_gui_run_as_admin")
func swext_gui_run_as_admin(shellScriptC: UnsafePointer<CChar>, promptC: UnsafePointer<CChar>) -> GResultErr {
    let shellScript = String(cString: shellScriptC)
    let prompt = String(cString: promptC)

    let escapedSh = shellScript.replacingOccurrences(of: "\\", with: "\\\\")
    .replacingOccurrences(of: "\"", with: "\\\"")
    let appleScript = "do shell script \"\(escapedSh)\" with administrator privileges with prompt \"\(prompt)\""
    let script = NSAppleScript(source: appleScript)
    guard script != nil else {
        return GResultErr(err: strdup("failed to create script"))
    }

    var error: NSDictionary?
    script?.executeAndReturnError(&error)
    if error != nil {
        return GResultErr(err: strdup(error?[NSAppleScript.errorMessage] as? String ?? "unknown error"))
    }

    return GResultErr(err: nil)
}

private func isFirefoxRecentlyUsed() -> Bool {
    return firefoxBundleIds.contains { bundleId in
        if let bundleUrl = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleId),
           let attributes = NSMetadataItem(url: bundleUrl),
           let date = attributes.value(forAttribute: kMDItemLastUsedDate as String) as? Date,
           date.timeIntervalSinceNow < 365 * 24 * 60 * 60 {
            return true
        } else {
            return false
        }
    }
}

@_cdecl("swext_import_firefox_certs")
func swext_import_firefox_certs() {
    // conds: firefox last used within a year
    // this avoids triggering suspicious filesystem accesses (if we check profile dates)
    if isFirefoxRecentlyUsed() {
        // open docs page
        NSWorkspace.shared.open(URL(string: "https://go.orbstack.dev/firefox-cert")!)
    }
}
