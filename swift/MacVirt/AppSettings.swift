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
        GeneralSettingsView()
            .padding(20)
            .frame(width: 450, height: 200)
            .navigationTitle("Settings")
    }
}