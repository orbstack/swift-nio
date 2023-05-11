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
        case docker
        case network
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
        }
        .frame(width: 475)
        .padding(20)
        .navigationTitle("Settings")
    }
}