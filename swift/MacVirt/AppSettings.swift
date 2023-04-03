//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Sparkle

struct AppSettings: View {
    let updaterController: SPUStandardUpdaterController

    private enum Tabs: Hashable {
        case general
        case machine
    }

    var body: some View {
        TabView {
            GeneralSettingsView(updaterController: updaterController)
                    .tabItem {
                        Label("General", systemImage: "gear")
                    }
                    .tag(Tabs.general)

            MachineSettingsView()
                    .tabItem {
                        Label("Machine", systemImage: "cpu")
                    }
                    .tag(Tabs.machine)
        }
        .frame(width: 475, height: 200)
        .padding(20)
        .navigationTitle("Settings")
    }
}