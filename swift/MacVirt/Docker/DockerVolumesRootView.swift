//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct DockerVolumesRootView: View {
    @Environment(\.controlActiveState) private var controlActiveState: ControlActiveState

    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var actionTracker: ActionTracker

    @Default(.dockerVolumesSortDescriptor) private var sortDescriptor

    @State private var selection: Set<String> = []

    var body: some View {
        let searchQuery = vmModel.searchText

        DockerStateWrapperView(\.dockerVolumes) { volumes, _ in
            let filteredVolumes = self.filterVolumes(volumes.values, searchQuery: searchQuery)

            // 0 spacing to fix bg color gap between list and getting started hint
            VStack(spacing: 0) {
                if !filteredVolumes.isEmpty {
                    let totalSizeFormatted = calcTotalSize(filteredVolumes)
                    let listData = [
                        AKSection("In Use", filteredVolumes.filter { vmModel.volumeIsMounted($0) }),
                        AKSection(
                            "Unused", filteredVolumes.filter { !vmModel.volumeIsMounted($0) }),
                    ]

                    AKList(listData, selection: $selection, rowHeight: 48) { volume in
                        // TODO: optimize: pass isMounted section info
                        DockerVolumeItem(volume: volume)
                            .id(volume.name)
                            .environmentObject(vmModel)
                            .environmentObject(windowTracker)
                            .environmentObject(actionTracker)
                    }
                    .if(totalSizeFormatted != nil) { list in
                        list.navigationSubtitle(totalSizeFormatted ?? "")
                    }
                    .onAppear {
                        maybeRefreshDf()
                    }
                    .onChange(of: controlActiveState) { state in
                        if state == .key {
                            maybeRefreshDf()
                        }
                    }
                    .inspectorSelection(selection)
                } else {
                    Spacer()

                    HStack {
                        Spacer()
                        if searchQuery.isEmpty {
                            ContentUnavailableViewCompat("No Volumes", systemImage: "externaldrive")
                        } else {
                            ContentUnavailableViewCompat.search
                        }
                        Spacer()
                    }

                    Spacer()
                }
            }
            .overlay(alignment: .bottomTrailing) {
                StatusOverlayBadge("Exporting", set: actionTracker.ongoingVolumeExports, publisher: actionTracker.$ongoingVolumeExports)
            }
        }
        .navigationTitle("Volumes")
        // SwiftUI bug: sheet in button keeps appearing and disappearing when searchable is there
        .sheet(isPresented: $vmModel.presentCreateVolume) {
            CreateVolumeView(isPresented: $vmModel.presentCreateVolume)
        }
    }

    private func calcTotalSize(_ filteredVolumes: [DKVolume]) -> String? {
        if let dockerDf = vmModel.dockerDf {
            let totalSize = filteredVolumes.reduce(Int64(0)) { acc, vol in
                if let usageData = dockerDf.volumes[vol.name]?.usageData {
                    return acc + usageData.size
                } else {
                    return acc
                }
            }
            let totalSizeFormatted = ByteCountFormatter.string(
                fromByteCount: totalSize, countStyle: .file)

            return "\(totalSizeFormatted) total"
        }

        return nil
    }

    private func filterVolumes(_ volumes: any Sequence<DKVolume>, searchQuery: String) -> [DKVolume] {
        var volumes = volumes.filter { volume in
            searchQuery.isEmpty || volume.name.localizedCaseInsensitiveContains(searchQuery)
        }
        volumes.sort(accordingTo: sortDescriptor, model: vmModel)
        return volumes
    }

    private func maybeRefreshDf() {
        // only refresh if we're missing df info for some volumes
        if let volumes = vmModel.dockerVolumes,
           volumes.values.contains(where: { vol in
                vmModel.dockerDf?.volumes[vol.name] == nil
            })
        {
            Task { @MainActor in
                await vmModel.tryDockerSystemDf()
            }
        }
    }
}
