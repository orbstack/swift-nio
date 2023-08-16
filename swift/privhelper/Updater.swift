//
//  Updater.swift
//  SwiftAuthorizationSample
//
//  Created by Josh Kaplan on 2021-10-24
//

import Foundation
import EmbeddedPropertyList

/// An in-place updater for the helper tool.
///
/// To keep things simple, this updater only works if `launchd` property lists do not change between versions.
enum Updater {
    /// Replaces itself with the helper tool located at the provided `URL` so long as security, launchd, and version requirements are met.
    ///
    /// - Parameter req: Path to the helper tool.
    /// - Throws: If the helper tool file can't be read, public keys can't be determined, or `launchd` property lists can't be compared.
    static func updateHelperTool(req: PHUpdateRequest) throws {
        NSLog("req: \(req)")
        guard try CodeInfo.doesPublicKeyMatch(forExecutable: req.helperURL) else {
            NSLog("bad signature")
            throw PHUpdateError.badSignature
        }

        let (curVersion, newVersion) = try readVersions(helperUrl: req.helperURL)
        if curVersion == newVersion {
            NSLog("same version")
            return
        } else if newVersion < curVersion {
            NSLog("downgrade")
            throw PHUpdateError.downgrade(from: curVersion.rawValue, to: newVersion.rawValue)
        }
        
        guard try checkLaunchdPlistMatches(helperUrl: req.helperURL) else {
            NSLog("launchd property lists don't match")
            throw PHUpdateError.launchdPlistChanged
        }

        try NSLog("current \(CodeInfo.currentCodeLocation())")
        try Data(contentsOf: req.helperURL).write(to: CodeInfo.currentCodeLocation(), options: .atomicWrite)
        // can't self-reexec. needs to be started by launchd for mach service
        NSLog("updated, restarting")
        exit(0)
    }

    private static func readVersions(helperUrl: URL) throws -> (BundleVersion, BundleVersion) {
        let current = try HelperToolInfoPlist.main.version
        let new = try HelperToolInfoPlist(from: helperUrl).version
        return (current, new)
    }

    private static func checkLaunchdPlistMatches(helperUrl: URL) throws -> Bool {
        try EmbeddedPropertyListReader.launchd.readInternal() ==
                EmbeddedPropertyListReader.launchd.readExternal(from: helperUrl)
    }
}
