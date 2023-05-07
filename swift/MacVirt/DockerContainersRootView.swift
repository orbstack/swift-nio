//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct ComposeGroup: Hashable, Equatable {
    let project: String
    let configFiles: String
    var anyRunning: Bool = false
}

private struct ListItem: Identifiable, Equatable {
    var builtinRecord: ContainerRecord? = nil
    var sectionLabel: String? = nil
    var container: DKContainer? = nil
    var composeGroup: ComposeGroup? = nil
    var children: [ListItem]? = nil

    var id: DockerContainerId {
        if let builtinRecord {
            return .notDocker(key: builtinRecord.id)
        }
        if let sectionLabel {
            return .notDocker(key: sectionLabel)
        }
        if let container {
            return .container(id: container.id)
        }
        if let composeGroup {
            return .compose(project: composeGroup.project, configFiles: composeGroup.configFiles)
        }
        return .notDocker(key: "")
    }

    var containerName: String {
        container?.names.first ?? composeGroup?.project ?? ""
    }

    init(builtinRecord: ContainerRecord) {
        self.builtinRecord = builtinRecord
    }

    init(sectionLabel: String) {
        self.sectionLabel = sectionLabel
    }

    init(container: DKContainer) {
        self.container = container
    }

    init(composeGroup: ComposeGroup, children: [ListItem]) {
        self.composeGroup = composeGroup
        self.children = children
    }
}

struct DockerContainersRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @AppStorage("docker_filterShowStopped") private var settingShowStopped = true

    @State private var selection: Set<DockerContainerId> = []
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
                VStack {
                    // MUST have VStack!
                    // otherwise each unused item in this group (3) is shown as an empty space for compose groups w/ children
                    if let builtinRecord = item.builtinRecord {
                        BuiltinContainerItem(record: builtinRecord)
                    }
                    if let sectionLabel = item.sectionLabel {
                        Text(sectionLabel)
                                .font(.subheadline.bold())
                                .foregroundColor(.secondary)
                    }
                    if let container = item.container {
                        DockerContainerItem(container: container, selection: selection)
                                .equatable()
                    }
                    if let composeGroup = item.composeGroup {
                        DockerComposeGroupItem(composeGroup: composeGroup, selection: selection)
                                .equatable()
                    }
                }
                .id(item.id)
            }
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
        // TODO - workaround was to remove section headers
        var listItems: [ListItem] = [
            //ListItem(builtinRecord: dockerRecord),
            //ListItem(sectionLabel: "Running"),
        ]
        var runningItems: [ListItem] = []
        var stoppedItems: [ListItem] = []

        // collect compose groups and remove them from containers
        var ungroupedContainers: [DKContainer] = []
        var composeGroups: [ComposeGroup: [DKContainer]] = [:]

        for container in filteredContainers {
            if let composeProject = container.labels[DockerLabels.composeProject],
               let configFiles = container.labels[DockerLabels.composeConfigFiles] {
                let group = ComposeGroup(project: composeProject, configFiles: configFiles)
                if composeGroups[group] == nil {
                    composeGroups[group] = [container]
                } else {
                    composeGroups[group]?.append(container)
                }
            } else {
                ungroupedContainers.append(container)
            }
        }

        // convert to list items
        for (group, containers) in composeGroups {
            let children = containers.map { ListItem(container: $0) }
            var item = ListItem(composeGroup: group, children: children)
            // if ANY container in the group is running, show the group as running
            if containers.contains(where: { $0.running }) {
                item.composeGroup?.anyRunning = true
                runningItems.append(item)
            } else {
                stoppedItems.append(item)
            }
        }

        // add ungrouped containers
        for container in ungroupedContainers {
            if container.running {
                runningItems.append(ListItem(container: container))
            } else {
                stoppedItems.append(ListItem(container: container))
            }
        }

        // sort by name within running/stopped sections
        runningItems.sort { $0.containerName < $1.containerName }
        stoppedItems.sort { $0.containerName < $1.containerName }

        // add running/stopped sections
        for item in runningItems {
            listItems.append(item)
        }
        if !stoppedItems.isEmpty {
            //listItems.append(ListItem(sectionLabel: "Stopped"))
            for item in stoppedItems {
                listItems.append(item)
            }
        }

        return listItems
    }
}
