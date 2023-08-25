//
//  CodeInfo.swift
//  SwiftAuthorizationSample
//
//  Created by Josh Kaplan on 2021-10-24
//

import Foundation

// allow all of the same team ID so this works in case of future bundle ID change
private let codeSigningReq = "anchor apple generic and certificate leaf[subject.OU] = \"HUAQ24HBR6\""

/// Convenience wrappers around Security framework functionality.
enum CodeInfo {
    /// Errors that may occur when trying to determine information about this running helper tool or another on disk executable.
    enum CodeInfoError: Error, Codable {
        case getSelfPath(OSStatus)
        case getExternalStaticCode(OSStatus)
        case getSelfStaticCode(OSStatus)
        case createCodeSigningRequirement(OSStatus)
    }

    /// Returns the on disk location this code is running from.
    ///
    /// - Throws: If unable to determine location.
    /// - Returns: On disk location of this helper tool.
    static func currentCodeLocation() throws -> URL {
        var path: CFURL?
        let status = SecCodeCopyPath(try copyCurrentStaticCode(), SecCSFlags(), &path)
        guard status == errSecSuccess, let path = path as URL? else {
            throw CodeInfoError.getSelfPath(status)
        }

        return path
    }

    // don't check public key match. cert could change
    static func matchTeamId(forExecutable executable: URL) throws -> Bool {
        // Only perform this comparison if the executable's static code has a valid signature
        let newStaticCode = try createStaticCode(forExecutable: executable)
        let flags = SecCSFlags(rawValue: kSecCSStrictValidate | kSecCSCheckAllArchitectures)
        var req: SecRequirement?
        guard SecRequirementCreateWithString(codeSigningReq as CFString, flags, &req) == errSecSuccess else {
            throw CodeInfoError.createCodeSigningRequirement(errSecInternalComponent)
        }
        return SecStaticCodeCheckValidity(newStaticCode, flags, req) == errSecSuccess
    }

    /// Convenience wrapper around `SecStaticCodeCreateWithPath`.
    ///
    /// - Parameter executable: On disk location of an executable.
    /// - Throws: If unable to create the static code.
    /// - Returns: Static code instance corresponding to the provided `URL`.
    static func createStaticCode(forExecutable executable: URL) throws -> SecStaticCode {
        var staticCode: SecStaticCode?
        let status = SecStaticCodeCreateWithPath(executable as CFURL, SecCSFlags(), &staticCode)
        guard status == errSecSuccess, let staticCode = staticCode else {
            throw CodeInfoError.getExternalStaticCode(status)
        }

        return staticCode
    }

    /// Convenience wrapper around `SecCodeCopySelf` and `SecCodeCopyStaticCode`.
    ///
    /// - Throws: If unable to create a copy of the on disk representation of this code.
    /// - Returns: Static code instance corresponding to the executable running this code.
    static func copyCurrentStaticCode() throws -> SecStaticCode {
        var currentCode: SecCode?
        let copySelfStatus = SecCodeCopySelf(SecCSFlags(), &currentCode)
        guard copySelfStatus == errSecSuccess, let currentCode = currentCode else {
            throw CodeInfoError.getSelfStaticCode(copySelfStatus)
        }

        var currentStaticCode: SecStaticCode?
        let staticCodeStatus = SecCodeCopyStaticCode(currentCode, SecCSFlags(), &currentStaticCode)
        guard staticCodeStatus == errSecSuccess, let currentStaticCode = currentStaticCode else {
            throw CodeInfoError.getSelfStaticCode(staticCodeStatus)
        }

        return currentStaticCode
    }
}
