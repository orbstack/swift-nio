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

            Spacer()
                    .frame(height: 20)

            UpdaterSettingsView(updater: updaterController.updater)
        }
        .padding()
    }
}
