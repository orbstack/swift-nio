//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import LaunchAtLogin
import Combine
import Sparkle
import Defaults

struct GeneralSettingsView: View {
    let updaterController: SPUStandardUpdaterController

    var body: some View {
        Form {
            LaunchAtLogin.Toggle {
                Text("Start at login")
            }
            Defaults.Toggle("Show in menu bar", key: .globalShowMenubarExtra)
            Defaults.Toggle("Stay in background when app is closed", key: .globalStayInBackground)

            UpdaterSettingsView(updater: updaterController.updater)

            #if DEBUG
            Button(action: {
                Defaults[.onboardingCompleted] = false
            }) {
                Text("reset onboarding")
            }
            #endif
        }
        .padding()
        .navigationTitle("Settings")
    }
}
