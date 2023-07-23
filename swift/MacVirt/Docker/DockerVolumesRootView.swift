//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerVolumesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: Set<String> = []
    @State private var searchQuery: String = ""

    var body: some View {
        DockerStateWrapperView(
            refreshAction: refresh
        ) { _, _ in
            if let volumes = vmModel.dockerVolumes {
                let filteredVolumes = volumes.filter { volume in
                    searchQuery.isEmpty ||
                            volume.name.localizedCaseInsensitiveContains(searchQuery)
                }

                // 0 spacing to fix bg color gap between list and getting started hint
                VStack(spacing: 0) {
                    if !filteredVolumes.isEmpty {
                        let totalSizeFormatted = calcTotalSize(filteredVolumes)

                        List(selection: $selection) {
                            Section(header: Text("In Use")) {
                                ForEach(filteredVolumes, id: \.name) { volume in
                                    if isMounted(volume) {
                                        DockerVolumeItem(volume: volume, isMounted: true, selection: selection)
                                                .id(volume.name)
                                    }
                                }
                            }

                            Section(header: Text("Unused")) {
                                ForEach(filteredVolumes, id: \.name) { volume in
                                    if !isMounted(volume) {
                                        DockerVolumeItem(volume: volume, isMounted: false, selection: selection)
                                                .id(volume.name)
                                    }
                                }
                            }

                            HStack {
                                Spacer()
                                VStack {
                                    Text("You can also find these volumes at ~/\(Folders.nfsName)/docker.")
                                            .font(.title3)
                                            .foregroundColor(.secondary)
                                            .multilineTextAlignment(.center)
                                }
                                        .padding(.vertical, 24)
                                Spacer()
                            }
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
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
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

    private func refresh() async {
        await vmModel.tryRefreshList()
        await vmModel.maybeTryRefreshDockerList(doSystemDf: true)
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
