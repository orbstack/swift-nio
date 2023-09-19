//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

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
            if let url = URL(string: "orbstack://docker/project-logs/\(projectB64URL)?base64=true") {
                NSWorkspace.shared.open(url)
            }
        } else {
            // find window by title and bring to front
            for window in NSApp.windows {
                if window.title == WindowTitles.projectLogs(project) {
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
            Button(action: {
                NSWorkspace.openSubwindow("docker/migration")
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
    let isFirstInList: Bool

    var body: some View {
        switch item {
        case .sectionLabel(let label):
            Text(label)
            .font(.subheadline.bold())
            .foregroundColor(.secondary)
        case .container(let container):
            DockerContainerItem(container: container, isFirstInList: isFirstInList)
            .equatable()
        case .compose(let group, _):
            DockerComposeGroupItem(composeGroup: group,
                    isFirstInList: isFirstInList)
            .equatable()
        case .k8sGroup(let group, _):
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
    let allContainers: [DKContainer]
    let listData: [AKSection<DockerListItem>]
    @Binding var selection: Set<DockerContainerId>

    let dockerImages: [DKImage]?
    let dockerVolumes: [DKVolume]?

    var body: some View {
        // TODO bad for perf
        let flatList = listData.flatMap { $0.items }

        VStack(spacing: 0) {
            if !listData.isEmpty {
                // icon = 32, + vertical 8 padding from item VStack = 48
                // (we used to do padding(4) + SwiftUI's auto list row padding of 4 = total 8 vertical padding, but that breaks double click region)
                // combined 4+4 padding is in DockerContainerListItemView to fix context menu bounds
                AKList(listData, selection: $selection, rowHeight: 32 + 8 + 8, flat: false) { item in
                    // single list row content item for perf: https://developer.apple.com/videos/play/wwdc2023/10160/
                    DockerContainerListItemView(item: item,
                            isFirstInList: item.id == flatList.first?.id)
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
                                dockerVolumes?.isEmpty == true &&
                                !filterIsSearch // not searching
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

    @State var selection: Set<DockerContainerId>
    @State var searchQuery: String

    var body: some View {
        DockerStateWrapperView(\.dockerContainers) { containers, dockerRecord in
            let runningCount = containers.filter { $0.running }.count

            let filteredContainers = containers.filter { container in
                searchQuery.isEmpty ||
                        container.id.localizedCaseInsensitiveContains(searchQuery) ||
                        container.image.localizedCaseInsensitiveContains(searchQuery) ||
                        container.imageId.localizedCaseInsensitiveContains(searchQuery) ||
                        container.names.first(where: { $0.localizedCaseInsensitiveContains(searchQuery) }) != nil
            }

            // 0 spacing to fix bg color gap between list and getting started hint
            let (runningItems, stoppedItems) = DockerContainerLists.makeListItems(filteredContainers: filteredContainers)
            let listData = makeListData(runningItems: runningItems, stoppedItems: stoppedItems)

            DockerContainersList(
                    filterIsSearch: !searchQuery.isEmpty,
                    runningCount: runningCount,
                    allContainers: containers,
                    listData: listData,
                    selection: $selection,

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

    private func makeListData(runningItems: [DockerListItem], stoppedItems: [DockerListItem]) -> [AKSection<DockerListItem>] {
        var listData = [AKSection<DockerListItem>]()

        if !runningItems.isEmpty {
            listData.append(AKSection(nil, runningItems))
        }

        if filterShowStopped && !stoppedItems.isEmpty {
            listData.append(AKSection("Stopped", stoppedItems))
        }

        return listData
    }
}
