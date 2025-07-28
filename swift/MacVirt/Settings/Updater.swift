//
// Created by Danny Lin on 2/8/23.
//

import Defaults
import Foundation
import Sparkle
import SwiftUI

// This view model class publishes when new updates can be checked by the user
final class CheckForUpdatesViewModel: ObservableObject {
    @Published var canCheckForUpdates = false

    init(updater: SPUUpdater) {
        updater.publisher(for: \.canCheckForUpdates)
            .assign(to: &$canCheckForUpdates)
    }
}

// This is the view for the Check for Updates menu item
// Note this intermediate view is necessary for the disabled state on the menu item to work properly before Monterey.
// See https://stackoverflow.com/questions/68553092/menu-not-updating-swiftui-bug for more info
struct CheckForUpdatesView: View {
    @ObservedObject private var checkForUpdatesViewModel: CheckForUpdatesViewModel
    private let updater: SPUUpdater

    init(updater: SPUUpdater) {
        self.updater = updater

        // Create our view model for our CheckForUpdatesView
        checkForUpdatesViewModel = CheckForUpdatesViewModel(updater: updater)
    }

    var body: some View {
        Button {
            updater.checkForUpdates()
        } label: {
            Label("Check for Updates…", systemImage: "arrow.down.circle")
        }
        .disabled(!checkForUpdatesViewModel.canCheckForUpdates)
    }
}

// This is the view for our updater settings
// It manages local state for checking for updates and automatically downloading updates
// Upon user changes to these, the updater's properties are set. These are backed by NSUserDefaults.
// Note the updater properties should *only* be set when the user changes the state.
struct UpdaterSettingsView: View {
    let updater: SPUUpdater
    @Default(.updatesOptinChannel) private var updatesOptinChannel

    @State private var automaticallyDownloadsUpdates = false

    var body: some View {
        Group {
            Toggle("Automatically download updates", isOn: $automaticallyDownloadsUpdates)
                .onAppear {
                    automaticallyDownloadsUpdates = updater.automaticallyDownloadsUpdates
                }
                .onChange(of: automaticallyDownloadsUpdates) { _, newValue in
                    updater.automaticallyDownloadsUpdates = newValue
                }

            Picker("Update channel", selection: $updatesOptinChannel) {
                Text("Stable").tag("stable")
                Text("Canary (faster)").tag("canary")
            }
        }
    }
}
