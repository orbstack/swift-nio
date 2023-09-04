//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerVolumesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var actionTracker: ActionTracker

    @State private var selection: Set<String> = []
    @State private var searchQuery: String = ""

    var body: some View {
        DockerStateWrapperView(\.dockerVolumes) { volumes, _ in
            let filteredVolumes = volumes.filter { volume in
                searchQuery.isEmpty ||
                        volume.name.localizedCaseInsensitiveContains(searchQuery)
            }

            // 0 spacing to fix bg color gap between list and getting started hint
            VStack(spacing: 0) {
                if !filteredVolumes.isEmpty {
                    let totalSizeFormatted = calcTotalSize(filteredVolumes)
                    let listData = [
                        AKSection("In Use", filteredVolumes.filter { isMounted($0) }),
                        AKSection("Unused", filteredVolumes.filter { !isMounted($0) })
                    ]

                    AKList(listData, selection: $selection, rowHeight: 48) { volume in
                        // TODO optimize: pass section info
                        DockerVolumeItem(volume: volume, isMounted: isMounted(volume))
                        .id(volume.name)
                        .environmentObject(vmModel)
                        .environmentObject(actionTracker)
                    }
                    .if(totalSizeFormatted != nil) { list in
                        list.navigationSubtitle(totalSizeFormatted ?? "")
                    }
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
        }
        .navigationTitle("Volumes")
        .searchable(
            text: $searchQuery,
            placement: .toolbar,
            prompt: "Search"
        )
        // SwiftUI bug: sheet in button keeps appearing and disappearing when searchable is there
        .sheet(isPresented: $vmModel.presentCreateVolume) {
            CreateVolumeView(isPresented: $vmModel.presentCreateVolume)
        }
    }

    private func isMounted(_ volume: DKVolume) -> Bool {
        guard let containers = vmModel.dockerContainers else {
            return false
        }

        return containers.first { container in
            container.mounts.contains { mount in
                mount.type == .volume &&
                    mount.name == volume.name
            }
        } != nil
    }

    private func calcTotalSize(_ filteredVolumes: [DKVolume]) -> String? {
        if let dockerDf = vmModel.dockerSystemDf {
            let totalSize = filteredVolumes.reduce(Int64(0)) { acc, vol in
                if let dfVolume = dockerDf.volumes.first(where: { $0.name == vol.name }),
                   let usageData = dfVolume.usageData {
                    return acc + usageData.size
                } else {
                    return acc
                }
            }
            let totalSizeFormatted = ByteCountFormatter.string(fromByteCount: totalSize, countStyle: .file)

            return "\(totalSizeFormatted) total"
        }

        return nil
    }
}
