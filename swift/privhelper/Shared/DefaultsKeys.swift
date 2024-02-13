//
// Created by Danny Lin on 2/8/23.
//

import Defaults
import Foundation
import SwiftUI

private let guiBundleId = "dev.kdrag0n.MacVirt"

// doesn't count as logged in
private let previewRefreshToken = "1181201e-23f8-41f6-9660-b7110f4bfedb"

enum EntitlementTier: Int, Codable {
    case none = 0
    case pro = 1
    case enterprise = 3

    var desc: String? {
        switch self {
        case .none:
            return nil
        case .pro:
            return "Pro"
        case .enterprise:
            return "Enterprise"
        }
    }
}

enum EntitlementType: Int, Codable {
    case none = 0
    case subMonthly = 1
    case subYearly = 2
    case trial = 3
}

enum EntitlementStatus: Int, Codable {
    case red = 0
    case yellow = 1
    case green = 2
}

class DrmState: Codable, Defaults.Serializable {
    var refreshToken: String?
    var entitlementTier: EntitlementTier
    var entitlementType: EntitlementType
    var entitlementMessage: String?
    var entitlementStatus: EntitlementStatus?

    // TODO: deal with mutation
    private lazy var claims: [String: Any] = decodeClaims() ?? [:]

    init(refreshToken: String? = nil, entitlementTier: EntitlementTier = .none,
         entitlementType: EntitlementType = .none, entitlementMessage: String? = nil,
         entitlementStatus: EntitlementStatus = .red)
    {
        self.refreshToken = refreshToken
        self.entitlementTier = entitlementTier
        self.entitlementType = entitlementType
        self.entitlementMessage = entitlementMessage
        self.entitlementStatus = entitlementStatus
    }

    private func decodeClaims() -> [String: Any]? {
        guard let refreshToken else {
            return nil
        }
        let parts = refreshToken.split(separator: ".")
        guard parts.count == 3 else {
            return nil
        }
        let claims = parts[1]
        guard let data = Data(base64URLEncoded: String(claims)) else {
            return nil
        }
        return try? JSONSerialization.jsonObject(with: data, options: []) as? [String: Any]
    }

    // TODO: err cond
    var imageURL: URL? {
        if let imageURL = claims["_uim"] as? String {
            return URL(string: imageURL)
        } else {
            return nil
        }
    }

    var title: String {
        if let title = claims["_unm"] as? String {
            return title
        } else if let email = claims["_uem"] as? String {
            // fallback to email. not all users have name
            return email.components(separatedBy: "@").first
                    ?? "(no name)"
        } else {
            // nothing
            return "Sign In"
        }
    }

    var expired: Bool {
        if let expiresAt = claims["exp"] as? TimeInterval {
            // NO leeway - to warn before vmgr does
            return Date.now > Date(timeIntervalSince1970: expiresAt)
        } else {
            return false
        }
    }

    var subtitle: String {
        if expired {
            return "Sign in again"
        }

        // 1. entitlement message
        if let entitlementMessage {
            return entitlementMessage
        }

        // 2. tier
        if let desc = entitlementTier.desc {
            if entitlementType == .trial {
                return "\(desc) Trial"
            } else {
                return desc
            }
        }

        // 3. Personal use only
        return "Personal use only"
    }

    var statusDotColor: Color {
        if expired {
            return .red
        }

        switch entitlementStatus {
        case .red:
            return .red
        case .yellow:
            return .yellow
        case .green:
            return .green
        case nil:
            // fallback for version upgrade w/ old token
            return entitlementType == .trial ? .yellow :
                    (entitlementTier == .none ? .red : .green)
        }
    }

    var isSignedIn: Bool {
        // expired = user should sign in again (force it in the case of MDM SSO enforcement)
        // apparently sometimes there's an empty/broken token causing UI state mismatch, so use title as canonical source
        refreshToken != nil && refreshToken != previewRefreshToken && !expired && title != "Sign In"
    }
}

extension Defaults.Keys {
    // shared with swext in vmgr, which may have diff bundle ID
    private static let suite = getDefaultsSuite()

    static let selectedTab = Key<String>("root_selectedTab", default: "docker", suite: suite)

//    static let dockerFilterShowStopped = Key<Bool>("docker_filterShowStopped", default: true, suite: suite)
    static let dockerMigrationDismissed = Key<Bool>("docker_migrationDismissed", default: false, suite: suite)

    static let logsWordWrap = Key<Bool>("logs_wordWrap", default: true, suite: suite)

//    static let k8sFilterShowSystemNs = Key<Bool>("k8s_filterShowSystemNs", default: false, suite: suite)

    static let onboardingCompleted = Key<Bool>("onboardingCompleted", default: false, suite: suite)

    // key changed because initial release was flaky
    static let tipsMenubarBgShown = Key<Bool>("tips_menubarBgShown2", default: false, suite: suite)
    static let tipsContainerDomainsShow = Key<Bool>("tips_containerDomainsShow", default: true, suite: suite)
    static let tipsContainerFilesShow = Key<Bool>("tips_containerFilesShow", default: true, suite: suite)
    static let tipsImageMountsShow = Key<Bool>("tips_imageMountsShow", default: true, suite: suite)

    static let globalShowMenubarExtra = Key<Bool>("global_showMenubarExtra", default: true, suite: suite)
    // changed key in v0.14.0: setting was renamed and people enabled it due to misunderstanding in prev versions
    static let globalStayInBackground = Key<Bool>("global_stayInBackground2", default: false, suite: suite)

    static let adminDismissCount = Key<Int>("admin_dismissCount", default: 0, suite: suite)

    static let updatesOptinChannel = Key<String>("updates_optinChannel", default: "stable", suite: suite)

    // set to -1 if user has
    static let networkHttpsDismissCount = Key<Int>("network_httpsDismissCount", default: 0, suite: suite)

    // login
    static let drmLastState = Key<DrmState?>("drm_lastState", default: nil, suite: suite)

    static let mdmSsoDomain = Key<String?>("mdm_ssoDomain", default: nil, suite: suite)

    private static func getDefaultsSuite() -> UserDefaults {
        // vmgr has different bundle id, depending on signing id
        if Bundle.main.bundleIdentifier == guiBundleId {
            return UserDefaults.standard
        } else {
            return UserDefaults(suiteName: guiBundleId)!
        }
    }
}

// https://stackoverflow.com/questions/39075043/how-to-convert-data-to-hex-string-in-swift
import Foundation

/// Extension for making base64 representations of `Data` safe for
/// transmitting via URL query parameters
extension Data {
    /// Instantiates data by decoding a base64url string into base64
    ///
    /// - Parameter string: A base64url encoded string
    init?(base64URLEncoded string: String) {
        self.init(base64Encoded: string.base64URLToBase64())
    }

    /// Encodes the string into a base64url safe representation
    ///
    /// - Returns: A string that is base64 encoded but made safe for passing
    ///            in as a query parameter into a URL string
    func base64URLEncodedString() -> String {
        return base64EncodedString().base64ToBase64URL()
    }
}

private extension String {
    func base64ToBase64URL() -> String {
        // Make base64 string safe for passing into URL query params
        let base64url = replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "=", with: "")
        return base64url
    }

    func base64URLToBase64() -> String {
        // Return to base64 encoding
        var base64 = replacingOccurrences(of: "_", with: "/")
            .replacingOccurrences(of: "-", with: "+")
        // Add any necessary padding with `=`
        if base64.count % 4 != 0 {
            base64.append(String(repeating: "=", count: 4 - base64.count % 4))
        }
        return base64
    }
}
