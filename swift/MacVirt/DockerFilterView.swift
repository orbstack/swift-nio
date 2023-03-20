//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerFilterView: View {
    @AppStorage("docker_filterShowStopped") private var settingShowStopped = false

    var body: some View {
        VStack(alignment: .leading) {
            Text("Filter")
                    .font(.headline.weight(.semibold))
                    .padding(.bottom, 8)

            Form {
                Section {
                    Toggle("Show stopped containers", isOn: $settingShowStopped)
                }
            }
        }
        .padding(20)
    }
}