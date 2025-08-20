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
                        let listData = filterMachines(machines.values, searchQuery: searchQuery)

                        if !listData.isEmpty {
                            // see DockerContainerItem for rowHeight calculation
                            AKList(listData, selection: $selection, rowHeight: 46) { info in
                                MachineItem(info: info)
                                    .environmentObject(vmModel)
                                    .environmentObject(windowTracker)
                                    .environmentObject(actionTracker)
                            }
                            .inspectorSelection(selection)
                        } else {
                            ContentUnavailableViewCompat.search
                        }
                    } else {
                        Spacer()
                        HStack {
                            Spacer()
                            VStack {
                                ContentUnavailableViewCompat(
                                    "No Machines", systemImage: "desktopcomputer"
                                ) {
                                    Button {
                                        vmModel.presentCreateMachine = true
                                    } label: {
                                        Text("New Machine")
                                            .padding(6)
                                    }
                                    .controlSize(.large)
                                    .keyboardShortcut(.defaultAction)
                                }
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
                                Button {
                                    selectedTab = .dockerContainers
                                } label: {
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
                    StatusOverlayBadge(
                        "Exporting", set: actionTracker.ongoingMachineExports,
                        publisher: actionTracker.$ongoingMachineExports)
                }
            } else {
                ProgressView()
            }
        }
        .navigationTitle("Machines")
    }

    private func filterMachines(_ machines: any Sequence<ContainerInfo>, searchQuery: String)
        -> [AKSection<ContainerInfo>]
    {
        var listData = [AKSection<ContainerInfo>]()

        var runningItems: [ContainerInfo] = []
        var stoppedItems: [ContainerInfo] = []
        for machine in machines {
            if !machine.record.builtin
                && (searchQuery.isEmpty
                    || machine.record.name.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.distro.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.variant.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.version.localizedCaseInsensitiveContains(searchQuery)
                    || machine.record.image.arch.localizedCaseInsensitiveContains(searchQuery))
            {
                if machine.record.running {
                    runningItems.append(machine)
                } else {
                    stoppedItems.append(machine)
                }
            }
        }

        // sort both
        runningItems.sort { $0.record.name < $1.record.name }
        stoppedItems.sort { $0.record.name < $1.record.name }

        if !runningItems.isEmpty {
            listData.append(AKSection(nil, runningItems))
        }

        if !stoppedItems.isEmpty {
            listData.append(AKSection("Stopped", stoppedItems))
        }

        return listData
    }
}
