//
// Created by Danny Lin on 2/8/23.
//

import Foundation
import Defaults

extension Defaults.Keys {
    static let selectedTab = Key<String>("root.selectedTab", default: "docker")

    static let dockerFilterShowStopped = Key<Bool>("docker_filterShowStopped", default: true)

    static let onboardingCompleted = Key<Bool>("onboardingCompleted", default: false)

    static let globalShowMenubarExtra = Key<Bool>("global.showMenubarExtra", default: true)
}

// to propagate changes to publisher
// TODO: why is this needed? it's missing default value
extension UserDefaults {
    @objc dynamic var globalShowMenubarExtra: Bool {
        get { bool(forKey: "global.showMenubarExtra") }
        set { setValue(newValue, forKey: "global.showMenubarExtra") }
    }
}