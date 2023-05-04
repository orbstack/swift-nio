//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerContainersRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @AppStorage("docker_filterShowStopped") private var settingShowStopped = true

    @State private var selection: String?
    @State private var searchQuery: String = ""

    var body: some View {
        DockerStateWrapperView(
            refreshAction: refresh
        ) { containers, dockerRecord in
            let runningCount = containers.filter { $0.running }.count
            let totalCount = containers.count

            let filteredContainers = containers.filter { container in
                searchQuery.isEmpty ||
                        container.id.localizedCaseInsensitiveContains(searchQuery) ||
                        container.image.localizedCaseInsensitiveContains(searchQuery) ||
                        container.imageID.localizedCaseInsensitiveContains(searchQuery) ||
                        container.names.first(where: { $0.localizedCaseInsensitiveContains(searchQuery) }) != nil
            }

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
                    ForEach(filteredContainers) { container in
                        if container.running {
                            DockerContainerItem(container: container)
                        }
                    }

                    // special case: show example http://localhost if only container is getting-started
                    let visibleCount = settingShowStopped ? totalCount : runningCount
                    if visibleCount == 0 || (visibleCount == 1 && containers[0].image == "docker/getting-started" && containers[0].running) {
                        HStack {
                            Spacer()
                            VStack {
                                if visibleCount == 0 {
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
                        ForEach(filteredContainers) { container in
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
        .searchable(
            text: $searchQuery,
            placement: .toolbar,
            prompt: "Search"
        )
    }

    private func refresh() async {
        await vmModel.tryRefreshList()

        // will cause feedback loop if docker is stopped
        // querying this will start it
        if let containers = vmModel.containers,
            let dockerContainer = containers.first(where: { $0.id == ContainerIds.docker }),
            dockerContainer.state != .stopped {
            await vmModel.tryRefreshDockerList()
        }
    }
}
