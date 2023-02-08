//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerRootView: View {
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?

    var body: some View {
        Group {
            if let machines = vmModel.containers, let containers = vmModel.dockerContainers {
                List(selection: $selection) {
                    Section {
                        ForEach(machines) { record in
                            if record.builtin {
                                BuiltinContainerItem(record: record)
                            }
                        }
                    }

                    Section(header: Text("Containers")) {
                        ForEach(containers) { container in
                            DockerContainerItem(container: container)
                        }
                    }
                }
                        .refreshable {
                            await vmModel.tryRefreshList()
                            await vmModel.tryRefreshDockerList()
                        }
                        .navigationTitle("Docker")
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
                        .navigationTitle("Docker")
            }
        }
        .onAppear {
            Task {
                await vmModel.tryRefreshList()
                await vmModel.tryRefreshDockerList()
            }
        }
        .onChange(of: controlActiveState) { state in
            if state == .key {
                Task {
                    await vmModel.tryRefreshList()
                    await vmModel.tryRefreshDockerList()
                }
            }
        }
    }
}