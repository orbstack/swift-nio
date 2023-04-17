//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerFilterView: View {
    @AppStorage("docker_filterShowStopped") private var settingShowStopped = true

    var body: some View {
        VStack(alignment: .leading) {
            Form {
                Section {
                    Toggle("Show stopped containers", isOn: $settingShowStopped)
                }
            }
        }
        .padding(20)
    }
}
