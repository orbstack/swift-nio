//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?

    var body: some View {
        if let containers = vmModel.containers {
            List(selection: $selection) {
                Section {
                    ForEach(containers) { container in
                        if container.builtin {
                            BuiltinContainerItem(record: container)
                        }
                    }
                }

                Section(header: Text("Containers")) {
                    ForEach(containers) { container in
                    }
                }
            }
            .refreshable {
                await vmModel.tryRefreshList()
            }
            .navigationTitle("Docker")
        } else {
            ProgressView(label: {
                Text("Loading")
            })
            .navigationTitle("Docker")
        }
    }
}