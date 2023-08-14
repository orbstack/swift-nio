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

struct PHManager {
    private let xpcClient: XPCClient

    init() {
        self.xpcClient = XPCClient.forMachService(named: PHShared.helperID)
    }

    /// Attempts to install the helper tool, requiring user authorization.
    func install() async throws {
        // don't block main thread
        try await Task.detached {
            do {
                try PrivilegedHelperManager.shared.authorizeAndBless(message: "Allow using admin privileges for enhanced compatibility?")
            } catch AuthorizationError.canceled {
                Defaults[.adminDismissCount] += 1
            }
        }.value
    }
    
    /// Attempts to update the helper tool by having the helper tool perform a self update.
    func update() async throws {
        try await xpcClient.sendMessage(PHUpdateRequest(helperURL: PHShared.bundledURL),
                to: PHShared.updateRoute)
        //TODO ignore case .failure(let error) = response  -> .connectionInterrupted
    }
    
    /// Attempts to uninstall the helper tool by having the helper tool uninstall itself.
    func uninstall() async throws {
        try await xpcClient.send(to: PHShared.uninstallRoute)
        //TODO ignore case .failure(let error) = response  -> .connectionInterrupted
    }

    func symlink(src: String, dest: String) async throws {
        try await xpcClient.sendMessage(PHSymlinkRequest(src: src, dest: dest),
                to: PHShared.symlinkRoute)
    }

    func checkInstalled() async -> Bool {
        // check with launchd
        do {
            try await runProcessChecked("/bin/launchctl", ["print", "system/\(PHShared.helperID)"])
            return true
        } catch {
            return false
        }
    }
}
