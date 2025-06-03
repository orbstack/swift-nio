//
// Created by Danny Lin on 2/5/23.
//

import Combine
import Defaults
import Foundation
import LaunchAtLogin
import Sparkle
import SwiftUI

struct GeneralSettingsView: View {
    let updaterController: SPUStandardUpdaterController

    @Default(.globalShowMenubarExtra) private var showMenubarExtra

    var body: some View {
        SettingsForm {
            Section {
                LaunchAtLogin.Toggle {
                    Text("Start at login")
                }

                Defaults.Toggle("Show in menu bar", key: .globalShowMenubarExtra)

                let bgLabel =
                    if showMenubarExtra {
                        "Keep running when menu bar app is quit"
                    } else {
                        "Keep running when app is quit"
                    }
                Defaults.Toggle(bgLabel, key: .globalStayInBackground)
            }

            Section {
                UpdaterSettingsView(updater: updaterController.updater)
            } header: {
                Text("Updates")
            }
        }
        .navigationTitle("General")
    }
}
