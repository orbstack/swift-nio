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
        Form {
            LaunchAtLogin.Toggle {
                Text("Start at login")
            }
            Defaults.Toggle("Show in menu bar", key: .globalShowMenubarExtra)
            // ZStack to avoid layout shift
            ZStack(alignment: .leading) {
                Defaults.Toggle("Keep running when menu bar app is quit", key: .globalStayInBackground)
                    .opacity(showMenubarExtra ? 1 : 0)
                Defaults.Toggle("Keep running when app is quit", key: .globalStayInBackground)
                    .opacity(showMenubarExtra ? 0 : 1)
            }

            Spacer()
                .frame(height: 20)

            UpdaterSettingsView(updater: updaterController.updater)
        }
        .padding()
    }
}
