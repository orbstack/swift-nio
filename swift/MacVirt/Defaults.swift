//
// Created by Danny Lin on 2/8/23.
//

import Foundation
import Defaults

extension Defaults.Keys {
    static let selectedTab = Key<String>("root.selectedTab", default: "docker")

    static let dockerFilterShowStopped = Key<Bool>("docker_filterShowStopped", default: true)

    static let onboardingCompleted = Key<Bool>("onboardingCompleted", default: false)
}