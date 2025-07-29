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
    @Default(.terminalDefaultApp) private var terminalDefaultApp
    @Default(.terminalTheme) private var terminalTheme

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

            Section {
                Picker(selection: $terminalTheme) {
                    Text("System").tag(TerminalThemePreference.def)
                    Text("Rosé Pine").tag(TerminalThemePreference.rosePine)
                } label: {
                    Text("Theme")
                }

                Picker(selection: $terminalDefaultApp) {
                    Text("Last used").tag(String?(nil))

                    Divider()

                    // can have duplicate bundle IDs, which breaks Picker
                    ForEach(InstalledApps.terminals.uniqued(on: { $0.id }).sorted(by: { $0.name < $1.name }), id: \.self.id) { term in
                        HStack {
                            Image(nsImage: term.icon)
                                .resizable()
                                .frame(width: 16, height: 16)
                            Text(term.name)
                        }.tag(term.id)
                    }
                } label: {
                    Text("External terminal app")
                    Text("Used when opening terminal in a new window.")
                }
            } header: {
                Text("Terminal")
            }
        }
        .akNavigationTitle("General")
    }
}
