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
            return .notDocker(key: "BUI:\(builtinRecord.id)")
        }
        if let sectionLabel {
            return .notDocker(key: "SEC:\(sectionLabel)")
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

    var isGroup: Bool {
        composeGroup != nil
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

private struct GettingStartedHintBox: View {
    var body: some View {
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

            let filteredContainers = containers.filter { container in
                searchQuery.isEmpty ||
                        container.id.localizedCaseInsensitiveContains(searchQuery) ||
                        container.image.localizedCaseInsensitiveContains(searchQuery) ||
                        container.imageID.localizedCaseInsensitiveContains(searchQuery) ||
                        container.names.first(where: { $0.localizedCaseInsensitiveContains(searchQuery) }) != nil
            }

            // 0 spacing to fix bg color gap between list and getting started hint
            VStack(spacing: 0) {
                let listItems = makeListItems(dockerRecord, filteredContainers)
                if !listItems.isEmpty {
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
                    // cover up SwiftUI bug: black bars on left/right sides of exiting rows when expanding group
                    // must use VisualEffectView for color Desktop Tinting
                    .overlay(
                            VisualEffectView(material: .contentBackground)
                                .frame(width: 10),
                            alignment: .leading
                    )
                    .overlay(
                            VisualEffectView(material: .contentBackground)
                                .frame(width: 10),
                            alignment: .trailing
                    )
                } else {
                    Spacer()

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

                    Spacer()

                    HStack {
                        Spacer()
                        GettingStartedHintBox()
                        Spacer()
                    }
                    .padding(.bottom, 64)
                }

                // special case: show example http://localhost if only visible container is getting-started
                // getting started hint box moves to bottom in this case
                if listItems.count == 1,
                   let container = listItems.first?.container,
                   container.image == "docker/getting-started" {
                    HStack {
                        Spacer()
                        GettingStartedHintBox()
                        Spacer()
                    }
                    .padding(.bottom, 64)
                    .background(VisualEffectView(material: .contentBackground))
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
        // and within each section, sort by isGroup first
        runningItems.sort { a, b in
            if a.isGroup != b.isGroup {
                return a.isGroup
            }
            return a.containerName < b.containerName
        }
        stoppedItems.sort { a, b in
            if a.isGroup != b.isGroup {
                return a.isGroup
            }
            return a.containerName < b.containerName
        }

        // add running/stopped sections
        listItems += runningItems
        if settingShowStopped && !stoppedItems.isEmpty {
            //listItems.append(ListItem(sectionLabel: "Stopped"))
            listItems += stoppedItems
        }

        return listItems
    }
}
