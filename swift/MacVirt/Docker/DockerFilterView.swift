//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct DockerFilterView: View {
    var body: some View {
        VStack(alignment: .leading) {
            Form {
                Section {
                    Defaults.Toggle("Show stopped containers", key: .dockerFilterShowStopped)
                }
            }
        }
        .padding(20)
    }
}
