//
// Created by Danny Lin on 8/4/23.
//

import Foundation
import SecureXPC

struct PHShared {
    static let symlinkRoute = XPCRoute.named("symlink")
            .withMessageType(PHSymlinkRequest.self)
            .throwsType(PHSymlinkError.self)
    static let uninstallRoute = XPCRoute.named("uninstall")
    static let updateRoute = XPCRoute.named("update")
            .withMessageType(PHUpdateRequest.self)
            .throwsType(PHUpdateError.self)

    // lazy init
    // = bundle ID = signing ID = binary name = XPC service name
    static let helperID = "dev.orbstack.OrbStack.privhelper"
    static let bundledURL = URL(fileURLWithPath: "\(Bundle.main.bundlePath)/Contents/Library/LaunchServices/\(helperID)")
    static let installedURL = URL(fileURLWithPath: "/Library/PrivilegedHelperTools/\(helperID)")
    static let installedPlistURL = URL(fileURLWithPath: "/Library/LaunchDaemons/\(helperID).plist")
}

struct PHSymlinkRequest: Codable {
    let src: String
    let dest: String
}

enum PHSymlinkError: Error, Codable {
    case pathNotAllowed
    case existingSocketLink
}

enum PHError: Error, Codable {
    case canceled
}

struct PHUpdateRequest: Codable {
    let helperURL: URL
}

enum PHUpdateError: Error, Codable {
    case badSignature
    case launchdPlistChanged
    case downgrade(from: String, to: String)
}
