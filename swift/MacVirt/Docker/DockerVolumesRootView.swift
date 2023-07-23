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
                    } else {
                        Spacer()

                        HStack {
                            Spacer()
                            ContentUnavailableViewCompat("No Volumes", systemImage: "externaldrive")
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
}
