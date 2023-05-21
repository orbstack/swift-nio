//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct DockerFilterView: View {
    @Default(.dockerFilterShowStopped) private var settingShowStopped

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
