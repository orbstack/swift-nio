//
// Created by Danny Lin on 5/24/23.
//

import AppKit
import Defaults
import Foundation

enum OnboardingManager {
    static func maybeStartOnboarding() {
        if !Defaults[.onboardingCompleted] {
            // to avoid confusion, disable menu bar until onboarding is completed
            Defaults[.globalShowMenubarExtra] = false

            for window in NSApp.windows {
                if window.isUserFacing {
                    // close breaks SwiftUI, causing it to randomly reopen old windows when showing an alert
                    // window.orderOut(nil)
                    window.close()
                }
            }

            NSWorkspace.openSubwindow("onboarding")
        }
    }
}
