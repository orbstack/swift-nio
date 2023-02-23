//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle

struct GeneralSettingsView: View {
    @AppStorage("onboardingCompleted") private var onboardingCompleted = false

    let updaterController: SPUStandardUpdaterController

    var body: some View {
        Form {
            LaunchAtLogin.Toggle {
                Text("Start at login")
            }

            UpdaterSettingsView(updater: updaterController.updater)

            #if DEBUG
            Button(action: {
                onboardingCompleted = false
            }) {
                Text("reset onboarding")
            }
            #endif
        }
        .padding()
        .navigationTitle("Settings")
    }
}
