//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Sparkle

struct AppSettings: View {
    let updaterController: SPUStandardUpdaterController

    @State private var selectedTab: Tabs = .general

    private enum Tabs: Hashable {
        case general
        case machine
        case docker
        case network
        case debug
    }

    var body: some View {
        TabView(selection: $selectedTab) {
            GeneralSettingsView(updaterController: updaterController)
                    .tabItem {
                        Label("General", systemImage: "gear")
                    }
                    .tag(Tabs.general)

            MachineSettingsView()
                    .tabItem {
                        Label("System", systemImage: "cpu")
                    }
                    .tag(Tabs.machine)

            DockerSettingsView()
                    .tabItem {
                        Label("Docker", systemImage: "shippingbox")
                    }
                    .tag(Tabs.docker)

            NetworkSettingsView()
                    .tabItem {
                        Label("Network", systemImage: "network")
                    }
                    .tag(Tabs.network)

            #if DEBUG
            DebugSettingsView(updaterController: updaterController)
                    .tabItem {
                        Label("Debug", systemImage: "hammer")
                    }
                    .tag(Tabs.debug)
            #endif
        }
        .frame(width: 475)
        .padding(20)
        .navigationTitle("Settings")
    }
}