//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct MachinesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var actionTracker: ActionTracker

    @State private var selection: Set<String> = []

    @Default(.selectedTab) private var selectedTab

    var body: some View {
        StateWrapperView {
            if let machines = vmModel.machines {
                VStack {
                    let searchQuery = vmModel.searchText

                    if machines.values.contains(where: { !$0.record.builtin }) {
                        let filteredContainers = filterMachines(machines.values, searchQuery: searchQuery)

                        if !filteredContainers.isEmpty {
                            // see DockerContainerItem for rowHeight calculation
                            AKList(filteredContainers, selection: $selection, rowHeight: 48) {
                                container in
                                MachineContainerItem(record: container.record)
                                    .environmentObject(vmModel)
                                    .environmentObject(windowTracker)
                                    .environmentObject(actionTracker)
                            }
                            .inspectorSelection(selection)
                        } else {
                            Spacer()
                            HStack {
                                Spacer()
                                ContentUnavailableViewCompat.search
                                Spacer()
                            }
                            Spacer()
                        }
                    } else {
                        Spacer()
                        HStack {
                            Spacer()
                            VStack {
                                ContentUnavailableViewCompat(
                                    "No Machines", systemImage: "desktopcomputer")

                                Button(action: {
                                    vmModel.presentCreateMachine = true
                                }) {
                                    Text("New Machine")
                                        .padding(6)
                                }
                                .controlSize(.large)
                                .keyboardShortcut(.defaultAction)
                            }
                            Spacer()
                        }
                        Spacer()

                        HStack {
                            Spacer()
                            VStack(spacing: 8) {
                                Text("Looking for Docker?")
                                    .font(.title3)
                                    .bold()
                                Text("You don’t need a Linux machine.")
                                    .font(.body)
                                    .padding(.bottom, 8)
                                Button(action: {
                                    selectedTab = .dockerContainers
                                }) {
                                    Text("Go to Containers")
                                }
                                .controlSize(.large)
                            }
                            .padding(.vertical, 24)
                            .padding(.horizontal, 48)
                            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
                            Spacer()
                        }
                        .padding(.bottom, 48)
                    }
                }
                .overlay(alignment: .bottomTrailing) {
                    StatusOverlayBadge("Exporting", set: actionTracker.ongoingMachineExports, publisher: actionTracker.$ongoingMachineExports)
                }
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
        .navigationTitle("Machines")
    }

    private func filterMachines(_ machines: any Sequence<ContainerInfo>, searchQuery: String) -> [ContainerInfo] {
        return machines.filter { machine in
            !machine.record.builtin && (
                searchQuery.isEmpty
                    || machine.record.name.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.distro.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.variant.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.version.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.arch.localizedCaseInsensitiveContains(searchQuery)
            )
        }
    }
}
