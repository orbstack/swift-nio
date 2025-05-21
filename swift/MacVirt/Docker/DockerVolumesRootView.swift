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
    @State private var exportingOpacity = 0.0

    var body: some View {
        let searchQuery = vmModel.searchText

        DockerStateWrapperView(\.dockerVolumes) { volumes, _ in
            let filteredVolumes = self.filterVolumes(volumes, searchQuery: searchQuery)

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
            .overlay(
                alignment: .bottomTrailing,
                content: {
                    HStack {
                        Text("Exporting")
                        ProgressView()
                            .scaleEffect(0.5)
                            .frame(width: 16, height: 16)
                    }
                    .padding(8)
                    .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
                    .opacity(exportingOpacity)
                    .padding(16)
                })
        }
        .navigationTitle("Volumes")
        // SwiftUI bug: sheet in button keeps appearing and disappearing when searchable is there
        .sheet(isPresented: $vmModel.presentCreateVolume) {
            CreateVolumeView(isPresented: $vmModel.presentCreateVolume)
        }
        .onAppear {
            exportingOpacity = actionTracker.ongoingVolumeExports.isEmpty ? 0 : 1
        }
        .onReceive(actionTracker.$ongoingVolumeExports) { exports in
            withAnimation {
                exportingOpacity = exports.isEmpty ? 0 : 1
            }
        }
    }

    private func calcTotalSize(_ filteredVolumes: [DKVolume]) -> String? {
        if let dockerDf = vmModel.dockerSystemDf {
            let totalSize = filteredVolumes.reduce(Int64(0)) { acc, vol in
                if let dfVolume = dockerDf.volumes.first(where: { $0.name == vol.name }),
                    let usageData = dfVolume.usageData
                {
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

    private func filterVolumes(_ volumes: [DKVolume], searchQuery: String) -> [DKVolume] {
        var volumes = volumes.filter { volume in
            searchQuery.isEmpty || volume.name.localizedCaseInsensitiveContains(searchQuery)
        }
        volumes.sort(accordingTo: sortDescriptor, model: vmModel)
        return volumes
    }

    private func maybeRefreshDf() {
        // only refresh if we're missing df info for some volumes
        if let volumes = vmModel.dockerVolumes,
            volumes.contains(where: { vol in
                vmModel.dockerSystemDf?.volumes
                    .first(where: { $0.name == vol.name }) == nil
            })
        {
            Task { @MainActor in
                await vmModel.tryDockerSystemDf()
            }
        }
    }
}
