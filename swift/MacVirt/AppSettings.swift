//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct AppSettings: View {
    private enum Tabs: Hashable {
        case general
    }

    var body: some View {
        TabView {
            GeneralSettingsView()
                    .tabItem {
                        Label("General", systemImage: "gear")
                    }
                    .tag(Tabs.general)
        }
        .padding(20)
        .frame(width: 375, height: 150)
        .navigationTitle("Settings")
    }
}