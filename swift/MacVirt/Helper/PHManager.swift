//
//  PrivHelperManager.swift
//  MacVirt
//
//  Created by Danny Lin on 8/3/23.
//

import Foundation
import Authorized
import Blessed
import EmbeddedPropertyList
import SecureXPC
import Defaults

class PHManager {
    private let xpcClient: XPCClient
    private var ready = false

    init() {
        self.xpcClient = XPCClient.forMachService(named: PHShared.helperID)
    }

    private func ensureReady() async throws {
        if ready {
            return
        }

        do {
            try await update()
        } catch XPCError.connectionInvalid {
            if await checkInstalled() {
                throw XPCError.connectionInvalid
            } else {
                try await install()
            }
        }
        ready = true
    }

    private func install() async throws {
        // don't block main thread
        try await Task.detached {
            do {
                try PrivilegedHelperManager.shared.authorizeAndBless(message: "Allow using admin privileges for enhanced compatibility?")
            } catch AuthorizationError.canceled {
                Defaults[.adminDismissCount] += 1
            }
        }.value
    }

    private func update() async throws {
        do {
            try await xpcClient.sendMessage(PHUpdateRequest(helperURL: PHShared.bundledURL),
                    to: PHShared.updateRoute)
        } catch XPCError.connectionInterrupted {
            // ignore: normal
        } catch PHUpdateError.downgrade {
            // ignore: normal - no upgrade needed
        }
    }
    
    /// Attempts to uninstall the helper tool by having the helper tool uninstall itself.
    func uninstall() async throws {
        if !(await checkInstalled()) {
            return
        }

        try await ensureReady()
        do {
            try await xpcClient.send(to: PHShared.uninstallRoute)
        } catch XPCError.connectionInterrupted {
            // ignore: normal
        }
    }

    func symlink(src: String, dest: String) async throws {
        try await ensureReady()
        try await xpcClient.sendMessage(PHSymlinkRequest(src: src, dest: dest),
                to: PHShared.symlinkRoute)
    }

    private func checkInstalled() async -> Bool {
        // check with launchd
        do {
            try await runProcessChecked("/bin/launchctl", ["print", "system/\(PHShared.helperID)"])
            return true
        } catch {
            return false
        }
    }
}
