//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct ComposeGroup: Hashable, Equatable {
    let project: String
    var anyRunning: Bool = false

    var cid: DockerContainerId {
        .compose(project: project)
    }
}

private struct GettingStartedHintBox: View {
    var body: some View {
        VStack(spacing: 8) {
            Text("Get started with an example")
                .font(.title2)
                .bold()
            Text("docker run -it -p 80:80 docker/getting-started")
                .font(.body.monospaced())
                .textSelection(.enabled)
            Text("Then open [localhost](http://localhost) in your browser.")
                .font(.body)
                .foregroundColor(.secondary)
        }
        .padding(.vertical, 24)
        .padding(.horizontal, 48)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct MigrationHintBox: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        VStack(spacing: 8) {
            Text("Migrate from Docker Desktop")
                .font(.title2)
                .bold()
            Text("Copy your existing containers, volumes, and images to OrbStack.")
                .font(.body)
                .padding(.bottom, 12)
            Button(action: {
                NSWorkspace.shared.open(URL(string: "orbstack://docker/migration")!)
            }) {
                Text("Migrate")
                    .padding(6)
            }
            .controlSize(.large)
            .keyboardShortcut(.defaultAction)
        }
        .padding(.vertical, 24)
        .padding(.horizontal, 48)
        .overlay(alignment: .topTrailing) {
            Button(action: {
                Defaults[.dockerMigrationDismissed] = true
            }) {
                Image(systemName: "xmark")
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .padding(8)
        }
        .background(colorScheme == .dark ? .ultraThickMaterial : .thickMaterial, in: RoundedRectangle(cornerRadius: 8))
        .background(Color(.systemPurple), in: RoundedRectangle(cornerRadius: 8))
    }
}

// need another view to fix type error
private struct DockerContainerListItemView: View {
    let item: DockerListItem
    let selection: Set<DockerContainerId>
    let initialSelection: Set<DockerContainerId>

    var body: some View {
        switch item {
        case .builtinRecord(let record):
            BuiltinContainerItem(record: record)
        case .sectionLabel(let label):
            Text(label)
            .font(.subheadline.bold())
            .foregroundColor(.secondary)
        case .container(let container):
            DockerContainerItem(container: container,
                    selection: selection,
                    presentPopover: initialSelection.contains(.container(id: container.id)))
            .equatable()
        case .compose(let group, _):
            DockerComposeGroupItem(composeGroup: group, selection: selection)
            .equatable()
        }
    }
}

private struct DockerContainersList: View {
    @Default(.dockerMigrationDismissed) private var dockerMigrationDismissed

    let filterShowStopped: Bool
    let filterIsSearch: Bool
    let runningCount: Int
    let allContainers: [DKContainer]
    let dockerRecord: ContainerRecord
    let listItems: [DockerListItem]
    let selection: Binding<Set<DockerContainerId>>
    let initialSelection: Set<DockerContainerId>

    let dockerImages: [DKImage]?
    let dockerVolumes: [DKVolume]?

    var body: some View {
        VStack(spacing: 0) {
            if !listItems.isEmpty {
                List(listItems, id: \.id, children: \.listChildren, selection: selection) { item in
                    // single list row content item for perf: https://developer.apple.com/videos/play/wwdc2023/10160/
                    DockerContainerListItemView(item: item,
                            selection: selection.wrappedValue,
                            initialSelection: initialSelection)
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
                    if filterIsSearch {
                        ContentUnavailableViewCompat.search
                    } else {
                        ContentUnavailableViewCompat("No Containers", systemImage: "shippingbox")
                    }
                    Spacer()
                }

                Spacer()

                // don't show getting started hint if empty is caused by filter
                if allContainers.isEmpty {
                    HStack {
                        Spacer()
                        // migration not previously done or dismissed
                        let isMigration = !dockerMigrationDismissed &&
                                // docker desktop recently used
                                InstalledApps.dockerDesktopRecentlyUsed &&
                                // containers, images, volumes all empty
                                allContainers.isEmpty &&
                                dockerImages?.isEmpty == true &&
                                dockerVolumes?.isEmpty == true
                        if isMigration {
                            MigrationHintBox()
                        } else {
                            GettingStartedHintBox()
                        }
                        Spacer()
                    }
                    .padding(.bottom, 48)
                }
            }
        }
                // show as overlay to avoid VisualEffectView affecting toolbar color
        .overlay {
            // special case: show example http://localhost if only visible container is getting-started
            // getting started hint box moves to bottom in this case
            if listItems.count == 1,
               case let .container(container) = listItems[0],
               container.image == "docker/getting-started" {

                VStack {
                    Spacer()
                    HStack {
                        Spacer()
                        GettingStartedHintBox()
                        Spacer()
                    }
                    .padding(.bottom, 48)
                    .background(VisualEffectView(material: .contentBackground))
                }
            }
        }
    }
}

struct DockerContainersRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @Default(.dockerFilterShowStopped) private var filterShowStopped

    let initialSelection: Set<DockerContainerId>
    @State var selection: Set<DockerContainerId>
    @State var searchQuery: String

    var body: some View {
        DockerStateWrapperView(
            refreshAction: refresh
        ) { containers, dockerRecord in
            let runningCount = containers.filter { $0.running }.count

            let filteredContainers = containers.filter { container in
                searchQuery.isEmpty ||
                        container.id.localizedCaseInsensitiveContains(searchQuery) ||
                        container.image.localizedCaseInsensitiveContains(searchQuery) ||
                        container.imageId.localizedCaseInsensitiveContains(searchQuery) ||
                        container.names.first(where: { $0.localizedCaseInsensitiveContains(searchQuery) }) != nil
            }

            // 0 spacing to fix bg color gap between list and getting started hint
            let listItems = DockerContainerLists.makeListItems(filteredContainers: filteredContainers,
                    dockerRecord: dockerRecord, showStopped: filterShowStopped)
            DockerContainersList(
                    filterShowStopped: filterShowStopped,
                    filterIsSearch: !searchQuery.isEmpty,
                    runningCount: runningCount,
                    allContainers: containers,
                    dockerRecord: dockerRecord,
                    listItems: listItems,
                    selection: $selection,
                    initialSelection: initialSelection,

                    dockerImages: vmModel.dockerImages,
                    dockerVolumes: vmModel.dockerVolumes
            )
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
        await vmModel.maybeTryRefreshDockerList()
    }
}
