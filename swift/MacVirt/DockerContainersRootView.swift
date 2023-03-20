//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerContainersRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @AppStorage("docker_filterShowStopped") private var settingShowStopped = false

    @State private var selection: String?

    var body: some View {
        DockerStateWrapperView(
            refreshAction: refresh
        ) { containers, dockerRecord in
            let runningCount = containers.filter { $0.running }.count

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

                Section(header: Text("Running")) {
                    ForEach(containers) { container in
                        if container.running {
                            DockerContainerItem(container: container)
                        }
                    }

                    // special case: show example http://localhost if only container is getting-started
                    if containers.isEmpty || (containers.count == 1 && containers[0].image == "docker/getting-started") {
                        HStack {
                            Spacer()
                            VStack {
                                if containers.isEmpty {
                                    Text("No containers")
                                            .font(.title)
                                            .foregroundColor(.secondary)
                                }

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

                if settingShowStopped {
                    Section(header: Text("Stopped")) {
                        ForEach(containers) { container in
                            if !container.running {
                                DockerContainerItem(container: container)
                            }
                        }
                    }
                }
            }
            .navigationSubtitle(runningCount == 0 ? "None running" : "\(runningCount) running")
        }
        .navigationTitle("Containers")
    }

    private func refresh() async {
        await vmModel.tryRefreshList()

        // will cause feedback loop if docker is stopped
        // querying this will start it
        if let containers = vmModel.containers,
            let dockerContainer = containers.first(where: { $0.name == "docker" }),
            dockerContainer.running {
            await vmModel.tryRefreshDockerList()
        }
    }
}
