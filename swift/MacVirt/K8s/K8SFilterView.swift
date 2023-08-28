//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct K8SFilterView: View {
    var body: some View {
        VStack(alignment: .leading) {
            Form {
                Section {
                    Defaults.Toggle("Show system namespace", key: .k8sFilterShowSystemNs)
                }
            }
        }
        .padding(20)
    }
}
