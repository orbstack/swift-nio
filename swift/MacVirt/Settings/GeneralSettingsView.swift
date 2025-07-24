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
    @Default(.defaultTerminalEmulator) private var defaultTerminalEmulator

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

                Picker(selection: $defaultTerminalEmulator) {
                    if InstalledApps.terminals.count > 0 {
                        ForEach(InstalledApps.terminals, id: \.self.id) { term in
                            Text(term.name).tag(term.id)
                        }
                        Divider()
                    }
                    Text("Last used").tag("")
                } label: {
                    Text("Terminal emulator")
                }.disabled(InstalledApps.terminals.count == 0)
            }

            Section {
                UpdaterSettingsView(updater: updaterController.updater)
            } header: {
                Text("Updates")
            }
        }
        .akNavigationTitle("General")
    }
}
