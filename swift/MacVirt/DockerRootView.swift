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
                if let machines = vmModel.containers,
                   let containers = vmModel.dockerContainers,
                   let dockerRecord = machines.first(where: { $0.builtin && $0.name == "docker" }) {
                    List(selection: $selection) {
                        if #available(macOS 13, *) {
                            Section {
                                BuiltinContainerItem(record: dockerRecord)
                            }
                        } else {
                            Section(header: Text("Features")) {
                                BuiltinContainerItem(record: dockerRecord)
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

                                        Spacer().frame(height: 64)

                                        VStack(spacing: 8) {
                                            Text("Get started with an example")
                                                    .font(.title3)
                                                    .bold()
                                            Text("docker run -it -p 80:80 docker/getting-started")
                                                    .font(.body.monospaced())
                                                    .textSelection(.enabled)
                                            Text("Then open [localhost](http://localhost) in your browser.")
                                                    .font(.body)
                                                    .foregroundColor(.secondary)
                                        }
                                        .padding(16)
                                        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
                                    }
                                    .padding(.top, 32)
                                    Spacer()
                                }
                            }
                        }
                    }
                    .onChange(of: dockerRecord.running) { _ in
                        Task {
                            await refresh()
                        }
                    }
                } else {
                    ProgressView(label: {
                        Text("Loading")
                    })
                }
            }
            .task {
                NSLog("refresh: docker task")
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
