//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

private struct ListItem: Identifiable {
    let builtinRecord: ContainerRecord?
    let sectionLabel: String?
    let container: DKContainer?
    let children: [ListItem]?

    var id: String {
        builtinRecord?.id ?? sectionLabel ?? container!.id
    }
}

struct DockerContainersRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @AppStorage("docker_filterShowStopped") private var settingShowStopped = true

    @State private var selection: Set<String> = []
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

            let listItems = makeListItems(dockerRecord, filteredContainers)
            List(listItems, id: \.id, children: \.children, selection: $selection) { item in
                if let builtinRecord = item.builtinRecord {
                    BuiltinContainerItem(record: builtinRecord)
                            .listRowInsets(EdgeInsets())
                }
                if let sectionLabel = item.sectionLabel {
                    Text(sectionLabel)
                            .font(.subheadline.bold())
                            .foregroundColor(.secondary)
                            .listRowInsets(EdgeInsets())
                }
                if let container = item.container {
                    DockerContainerItem(container: container)
                            .equatable()
                            .listRowInsets(EdgeInsets())
                }
            }
                    .listRowInsets(EdgeInsets())
            .navigationSubtitle(runningCount == 0 ? "None running" : "\(runningCount) running")

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

    private func makeListItems(_ dockerRecord: ContainerRecord, _ filteredContainers: [DKContainer]) -> [ListItem] {
        var listItems = [
            ListItem(builtinRecord: dockerRecord, sectionLabel: nil, container: nil, children: nil),
            ListItem(builtinRecord: nil, sectionLabel: "Running", container: nil, children: nil),
        ]

        // add running containers
        for container in filteredContainers {
            if container.running {
                listItems.append(ListItem(builtinRecord: nil, sectionLabel: nil, container: container, children: nil))
            }
        }

        // add stopped containers
        if settingShowStopped {
            listItems.append(ListItem(builtinRecord: nil, sectionLabel: "Stopped", container: nil, children: nil))
            for container in filteredContainers {
                if !container.running {
                    listItems.append(ListItem(builtinRecord: nil, sectionLabel: nil, container: container, children: nil))
                }
            }
        }

        return listItems
    }
}
