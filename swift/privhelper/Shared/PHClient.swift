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

class PHClient {
    private let xpcClient: XPCClient
    private var ready = false
    private var canceledInstall = false

    var installReason = "Allow using admin to improve compatibility?"

    init() {
        self.xpcClient = XPCClient.forMachService(named: PHShared.helperID)
    }

    private func ensureReady() async throws {
        if canceledInstall {
            throw PHError.canceled
        }
        if ready {
            return
        }
        defer {
            // whatever failed (probably canceled), it's probably not gonna recover
            ready = true
        }

        do {
            try await update()
        } catch XPCError.connectionInvalid {
            if await checkInstalled() {
                throw XPCError.connectionInvalid
            } else {
                do {
                    try await install()
                } catch PHError.canceled {
                    canceledInstall = true
                    throw PHError.canceled
                }
            }
        }
    }

    private func install() async throws {
        // don't block main thread
        NSLog("installing privhelper")
        try await Task.detached { [self] in
            do {
                // TODO: support new API + migration
                try PrivilegedHelperManager.shared.authorizeAndBless(message: installReason)
            } catch AuthorizationError.canceled {
                Defaults[.adminDismissCount] += 1
                throw PHError.canceled
            }
        }.value
    }

    private func update() async throws {
        do {
            try await xpcClient.sendMessage(PHUpdateRequest(helperURL: PHShared.bundledURL),
                    to: PHShared.updateRoute)
        } catch XPCError.connectionInterrupted {
            // ignore: normal
            NSLog("updated privhelper")
        } catch PHUpdateError.downgrade {
            // ignore: normal - no upgrade needed
        }
    }
    
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

        // reset
        ready = false
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
