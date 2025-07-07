//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct ComposeGroup: Hashable, Equatable {
    let project: String
    var anyRunning: Bool = false
    var isFullCompose: Bool = false

    var cid: DockerContainerId {
        .compose(project: project)
    }

    @MainActor
    func showLogs(windowTracker: WindowTracker) {
        if !windowTracker.openDockerLogWindowIds.contains(.compose(project: project)) {
            // workaround: url can't contain "domain"???
            let projectB64URL = project.data(using: .utf8)!.base64URLEncodedString()
            if let url = URL(string: "orbstack://docker/project-logs/\(projectB64URL)?base64=true")
            {
                NSWorkspace.shared.open(url)
            }
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == project && window.subtitle == WindowTitles.projectLogsBase {
                    window.makeKeyAndOrderFront(nil)
                    break
                }
            }
        }
    }
}

private struct GettingStartedHintBox: View {
    var body: some View {
        VStack(spacing: 8) {
            Text("Get started with an example")
                .font(.title2)
                .bold()
            CopyableText("docker run -it -p 80:80 docker/getting-started")
                .font(.body.monospaced())
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
            Button {
                NSWorkspace.openSubwindow(WindowID.migrateDocker)
            } label: {
                Text("Migrate")
                    .padding(6)
            }
            .controlSize(.large)
            .keyboardShortcut(.defaultAction)
        }
        .padding(.vertical, 24)
        .padding(.horizontal, 48)
        .overlay(alignment: .topTrailing) {
            Button {
                Defaults[.dockerMigrationDismissed] = true
            } label: {
                Image(systemName: "xmark")
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .padding(8)
        }
        .background(
            colorScheme == .dark ? .ultraThickMaterial : .thickMaterial,
            in: RoundedRectangle(cornerRadius: 8)
        )
        .background(Color(.systemPurple), in: RoundedRectangle(cornerRadius: 8))
    }
}

// need another view to fix type error
private struct DockerContainerListItemView: View {
    let item: DockerListItem
    let isFirstInList: Bool

    var body: some View {
        switch item {
        case let .sectionLabel(label):
            Text(label)
                .font(.subheadline.bold())
                .foregroundColor(.secondary)
        case let .container(container):
            DockerContainerItem(container: container, isFirstInList: isFirstInList)
                .equatable()
        case let .compose(group, children):
            DockerComposeGroupItem(
                composeGroup: group,
                children: children,
                isFirstInList: isFirstInList
            )
            .equatable()
        case let .k8sGroup(group, _):
            DockerK8sGroupItem(group: group)
                .equatable()
        }
    }
}

private struct DockerContainersList: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var actionTracker: ActionTracker

    @Default(.dockerMigrationDismissed) private var dockerMigrationDismissed

    let filterIsSearch: Bool
    let runningCount: Int
    let allContainersEmpty: Bool
    let listData: [AKSection<DockerListItem>]
    @Binding var selection: Set<DockerContainerId>

    let imagesAndVolumesEmpty: Bool

    var body: some View {
        // TODO: bad for perf
        let flatList = listData.flatMap { $0.items }

        VStack(spacing: 0) {
            if !listData.isEmpty {
                // icon = 32, + vertical 8 padding from item VStack = 48
                // (we used to do padding(4) + SwiftUI's auto list row padding of 4 = total 8 vertical padding, but that breaks double click region)
                // combined 4+4 padding is in DockerContainerListItemView to fix context menu bounds
                AKList(
                    listData, selection: $selection, rowHeight: 32 + 8 + 8, flat: false,
                    autosaveName: Defaults.Keys.docker_autosaveOutline
                ) { item in
                    // single list row content item for perf: https://developer.apple.com/videos/play/wwdc2023/10160/
                    DockerContainerListItemView(
                        item: item,
                        isFirstInList: item.id == flatList.first?.id
                    )
                    // environment must be re-injected across boundary
                    .environmentObject(vmModel)
                    .environmentObject(windowTracker)
                    .environmentObject(actionTracker)
                }
                .navigationSubtitle(runningCount == 0 ? "None running" : "\(runningCount) running")
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
                if allContainersEmpty {
                    HStack {
                        Spacer()
                        // migration not previously done or dismissed
                        let isMigration =
                            !dockerMigrationDismissed
                            // docker desktop recently used
                            && InstalledApps.dockerDesktopRecentlyUsed
                            // containers, images, volumes all empty
                            && allContainersEmpty && imagesAndVolumesEmpty && !filterIsSearch  // not searching
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
            if flatList.count == 1,
                case let .container(container) = flatList[0],
                container.image == "docker/getting-started"
            {
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

    @Default(.dockerFilterShowStopped) private var dockerFilterShowStopped
    @Default(.dockerContainersSortDescriptor) private var sortDescriptor

    @State var selection: Set<DockerContainerId>

    var body: some View {
        let searchQuery = vmModel.searchText

        DockerStateWrapperView(\.dockerContainers) { containers, _ in
            let runningCount = containers.byId.values.count { $0.running }

            let filteredContainers = filterContainers(
                containers.byId.values, searchQuery: searchQuery)

            // 0 spacing to fix bg color gap between list and getting started hint
            let (runningItems, stoppedItems) = DockerContainerLists.makeListItems(
                filteredContainers: filteredContainers,
                dockerFilterShowStopped: dockerFilterShowStopped)
            let listData = makeListData(runningItems: runningItems, stoppedItems: stoppedItems)

            DockerContainersList(
                filterIsSearch: !searchQuery.isEmpty,
                runningCount: runningCount,
                allContainersEmpty: containers.byId.isEmpty,
                listData: listData,
                selection: $selection,

                imagesAndVolumesEmpty: (vmModel.dockerImages?.isEmpty ?? true)
                    && (vmModel.dockerVolumes?.isEmpty ?? true)
            )
            .inspectorSelection(selection)
        }
        .navigationTitle("Containers")
        .sheet(isPresented: $vmModel.presentCreateContainer) {
            CreateContainerView(isPresented: $vmModel.presentCreateContainer)
        }
    }

    private func filterContainers(_ containers: any Sequence<DKContainer>, searchQuery: String)
        -> [DKContainer]
    {
        var containers = containers.filter { container in
            searchQuery.isEmpty
                || container.id.localizedCaseInsensitiveContains(searchQuery)
                || container.image.localizedCaseInsensitiveContains(searchQuery)
                || container.imageId.localizedCaseInsensitiveContains(searchQuery)
                || container.names.contains { $0.localizedCaseInsensitiveContains(searchQuery) }
        }
        containers.sort(accordingTo: sortDescriptor)
        return containers
    }

    private func makeListData(runningItems: [DockerListItem], stoppedItems: [DockerListItem])
        -> [AKSection<DockerListItem>]
    {
        var listData = [AKSection<DockerListItem>]()

        if !runningItems.isEmpty {
            listData.append(AKSection(nil, runningItems))
        }

        if dockerFilterShowStopped && !stoppedItems.isEmpty {
            listData.append(AKSection("Stopped", stoppedItems))
        }

        return listData
    }
}
