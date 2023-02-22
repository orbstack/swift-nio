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
            Group {
                if let machines = vmModel.containers, let containers = vmModel.dockerContainers {
                    List(selection: $selection) {
                        if #available(macOS 13, *) {
                            Section {
                                ForEach(machines) { record in
                                    if record.builtin {
                                        BuiltinContainerItem(record: record)
                                    }
                                }
                            }
                        } else {

                            Section(header: Text("Features")) {
                                ForEach(machines) { record in
                                    if record.builtin {
                                        BuiltinContainerItem(record: record)
                                    }
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
                                        Text("No containers")
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
                                print("try refresh: docker refreshable")
                                await refresh()
                            }
                } else {
                    ProgressView(label: {
                        Text("Loading")
                    })
                }
            }
            .task {
                print("try refresh: docker task")
                await refresh()
            }
            .onChange(of: controlActiveState) { state in
                if state == .key {
                    Task {
                        await refresh()
                    }
                }
            }
        }
        .navigationTitle("Docker")
    }

    private func refresh() async {
        await vmModel.tryRefreshList()

        // will cause feedback loop if docker is stopped
        // querying this will start it
        if let containers = vmModel.containers,
            let dockerContainer = containers.first(where: { $0.name == "docker" }),
            dockerContainer.running {
            await vmModel.tryRefreshDockerList()
        } else {
            vmModel.dockerContainers = []
        }
    }
}
