//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle
import Defaults

struct DebugSettingsView: View {
    let updaterController: SPUStandardUpdaterController

    var body: some View {
        Form {
            Button(action: {
                Defaults[.onboardingCompleted] = false
            }) {
                Text("reset onboarding")
            }
            Button(action: {
                Defaults[.tipsMenubarBgShown] = false
                Defaults[.dockerMigrationDismissed] = false
            }) {
                Text("reset tips")
            }
        }
        .padding()
    }
}
