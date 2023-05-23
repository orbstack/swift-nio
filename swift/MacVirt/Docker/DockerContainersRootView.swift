//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct ComposeGroup: Hashable, Equatable {
    let project: String
    let configFiles: String
    var anyRunning: Bool = false

    var cid: DockerContainerId {
        .compose(project: project, configFiles: configFiles)
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
            VStack(spacing: 0) {
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
                                DockerContainerItem(container: container,
                                        selection: selection,
                                        presentPopover: initialSelection.contains(.container(id: container.id)))
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

                    // don't show getting started hint if empty is caused by filter
                    let unfilteredListItems = DockerContainerLists.makeListItems(filteredContainers: containers,
                            dockerRecord: dockerRecord, showStopped: filterShowStopped)
                    if unfilteredListItems.isEmpty {
                        HStack {
                            Spacer()
                            GettingStartedHintBox()
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
                   let container = listItems.first?.container,
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
