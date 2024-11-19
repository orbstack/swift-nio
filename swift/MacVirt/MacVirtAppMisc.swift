//
//  MacVirtApp.swift
//  MacVirt
//
//  Created by Danny Lin on 1/11/23.
//

import Defaults
import Sparkle
import SwiftUI

class UpdateDelegate: NSObject, SPUUpdaterDelegate {
    private func readInstallID() -> UUID {
        // match file like vmgr drm/device.go
        do {
            let oldID = try String(contentsOfFile: Files.installId)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            // try to parse it as UUID
            if let uuid = UUID(uuidString: oldID) {
                return uuid
            }
        } catch {
            // fallthrough
        }

        // write a new one
        let newID = UUID()
        do {
            try newID.uuidString
                .lowercased()
                .write(toFile: Files.installId, atomically: false, encoding: .utf8)
        } catch {
            NSLog("failed to write install ID: \(error)")
        }
        return newID
    }

    func feedURLString(for _: SPUUpdater) -> String? {
        // installID % 100
        let uuidBytes = readInstallID().uuid
        // take a big endian uint32 of the first 4 bytes
        let id4 =
            (UInt32(uuidBytes.0) << 24) | (UInt32(uuidBytes.1) << 16) | (UInt32(uuidBytes.2) << 8)
            | UInt32(uuidBytes.3)
        let bucket = id4 % 100

        #if arch(arm64)
            return "https://api-updates.orbstack.dev/arm64/appcast.xml?bucket=\(bucket)"
        #else
            return "https://api-updates.orbstack.dev/amd64/appcast.xml?bucket=\(bucket)"
        #endif
    }

    func allowedChannels(for _: SPUUpdater) -> Set<String> {
        Set(["stable", Defaults[.updatesOptinChannel]])
    }

    func updaterWillRelaunchApplication(_: SPUUpdater) {
        // bypass menu bar termination hook
        AppLifecycle.forceTerminate = true

        // run post-update script if needed to repair
        if let script = Bundle.main.path(forAuxiliaryExecutable: "hooks/_postupdate") {
            do {
                let task = try Process.run(
                    URL(fileURLWithPath: script), arguments: [Bundle.main.bundlePath])
                task.waitUntilExit()
            } catch {
                print("Failed to run post-update script: \(error)")
            }
        }
    }
}

enum AppLifecycle {
    static var forceTerminate = false
}

enum WindowID {
    static let main = "main"
    static let signIn = "signin"
    static let feedback = "feedback"
    static let migrateDocker = "migratedocker"
    static let onboarding = "onboarding"
    static let diagReport = "diagreport"
    static let bugReport = "bugreport"
}

enum WindowURL {
    // fake windows opened by URL handler in AppDelegate
    // some are used by vmgr
    static let update = "update"
    static let completeAuth = "complete_auth"
    static let settings = "settings"
}

func getConfigDir() -> String {
    let home = FileManager.default.homeDirectoryForCurrentUser.path
    return home + "/.orbstack"
}

// WA: on macOS 13, openWindow() and handlesExternalEvents() don't do anything when opening a Window() from a non-SwiftUI context
// so we have to use WindowGroup() and avoid opening duplicate windows, in order to simulate singletons
// this is just a marker type for future migration
typealias SingletonWindowGroup = WindowGroup
