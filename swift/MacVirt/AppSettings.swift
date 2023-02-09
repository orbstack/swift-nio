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
    }

    var body: some View {
        GeneralSettingsView(updaterController: updaterController)
            .padding()
            .frame(width: 450)
            .navigationTitle("Settings")
    }
}