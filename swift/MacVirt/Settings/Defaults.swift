//
// Created by Danny Lin on 2/8/23.
//

import Foundation
import Defaults

extension Defaults.Keys {
    static let selectedTab = Key<String>("root_selectedTab", default: "docker")

    static let dockerFilterShowStopped = Key<Bool>("docker_filterShowStopped", default: true)

    static let onboardingCompleted = Key<Bool>("onboardingCompleted", default: false)

    static let tipsMenubarBgShown = Key<Bool>("tips_menubarBgShown", default: false)

    static let globalShowMenubarExtra = Key<Bool>("global_showMenubarExtra", default: true)
    static let globalStayInBackground = Key<Bool>("global_stayInBackground", default: false)
}
