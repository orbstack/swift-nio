//
// Created by Danny Lin on 2/8/23.
//

import Foundation
import Defaults

enum EntitlementTier: Int, Codable {
    case none = 0
    case pro = 1
    case enterprise = 3
}

enum EntitlementType: Int, Codable {
    case none = 0
    case subMonthly = 1
    case subYearly = 2
}

struct DrmState: Codable, Defaults.Serializable {
    var refreshToken: String?
    var entitlementTier: EntitlementTier
    var entitlementType: EntitlementType
    var entitlementMessage: String?

    init(refreshToken: String? = nil, entitlementTier: EntitlementTier = .none, entitlementType: EntitlementType = .none, entitlementMessage: String? = nil) {
        self.refreshToken = refreshToken
        self.entitlementTier = entitlementTier
        self.entitlementType = entitlementType
        self.entitlementMessage = entitlementMessage
    }

    //TODO deal with mutation
    private lazy var claims: [String: Any]? = {
        guard let refreshToken else {
            return nil
        }
        let parts = refreshToken.split(separator: ".")
        guard parts.count == 3 else {
            return nil
        }
        let claims = parts[1]
        guard let data = Data(base64Encoded: String(claims) + "==") else {
            return nil
        }
        return try? JSONSerialization.jsonObject(with: data, options: []) as? [String: Any]
    }()

    //TODO err cond
    var imageURL: URL? {
        mutating get {
            if let claims,
               let imageURL = claims["imageURL"] as? String {
                return URL(string: imageURL)
            } else {
                return nil
            }
        }
    }

    var title: String? {
        mutating get {
            if let claims,
               let title = claims["_unm"] as? String {
                return title
            } else {
                return nil
            }
        }
    }

    var subtitle: String? {
        // 1. entitlement message
        if let entitlementMessage {
            return entitlementMessage
        }

        // 2. tier
        switch entitlementTier {
        case .none:
            return nil
        case .pro:
            return "Pro"
        case .enterprise:
            return "Enterprise"
        }

        // 3. Personal use only
    }
}

extension Defaults.Keys {
    static let selectedTab = Key<String>("root_selectedTab", default: "docker")

    static let dockerFilterShowStopped = Key<Bool>("docker_filterShowStopped", default: true)
    static let dockerMigrationDismissed = Key<Bool>("docker_migrationDismissed", default: false)

    static let k8sFilterShowSystemNs = Key<Bool>("k8s_filterShowSystemNs", default: false)

    static let onboardingCompleted = Key<Bool>("onboardingCompleted", default: false)

    // key changed because initial release was flaky
    static let tipsMenubarBgShown = Key<Bool>("tips_menubarBgShown2", default: false)
    static let tipsContainerDomainsShow = Key<Bool>("tips_containerDomainsShow", default: true)
    static let tipsImageMountsShow = Key<Bool>("tips_imageMountsShow", default: true)

    static let globalShowMenubarExtra = Key<Bool>("global_showMenubarExtra", default: true)
    // changed key in v0.14.0: setting was renamed and people enabled it due to misunderstanding in prev versions
    static let globalStayInBackground = Key<Bool>("global_stayInBackground2", default: false)

    static let adminDismissCount = Key<Int>("admin_dismissCount", default: 0)

    static let updatesOptinChannel = Key<String>("updates_optinChannel", default: "stable")

    // login
    static let drmLastState = Key<DrmState?>("drm_lastState", default: nil)
}
