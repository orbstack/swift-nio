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
        StateWrapperView {
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

                        if containers.isEmpty {
                            HStack {
                                Spacer()
                                VStack {
                                    Text("No Docker containers")
                                            .font(.title)
                                            .foregroundColor(.secondary)
                                }
                                        .padding(.top, 32)
                                Spacer()
                            }
                        }
                    }
                }
                        .refreshable {
                            await vmModel.tryRefreshList()
                            await vmModel.tryRefreshDockerList()
                        }
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
        .task {
            await vmModel.tryRefreshList()
            await vmModel.tryRefreshDockerList()
        }
        .onChange(of: controlActiveState) { state in
            if state == .key {
                Task {
                    await vmModel.tryRefreshList()
                    await vmModel.tryRefreshDockerList()
                }
            }
        }
        .navigationTitle("Docker")
    }
}
