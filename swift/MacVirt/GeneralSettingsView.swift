//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct GeneralSettingsView: View {
    @AppStorage("startOnLogin") private var startOnLogin = true
    @AppStorage("memoryMib") private var memoryMib = 12.0

    var body: some View {
        Form {
            Toggle("Start on Login", isOn: $startOnLogin)
            Slider(value: $memoryMib, in: 1024...(48*1024)) {
                Text("Memory (\(memoryMib, specifier: "%.0f") MiB)")
            }
        }
        .padding(20)
        .frame(width: 350, height: 100)
        .navigationTitle("Settings")
    }
}
