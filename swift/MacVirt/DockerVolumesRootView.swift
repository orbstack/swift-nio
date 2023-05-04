//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerVolumesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?
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

                List(selection: $selection) {
                    Section(header: Text("In Use")) {
                        ForEach(filteredVolumes) { volume in
                            if isMounted(volume) {
                                DockerVolumeItem(volume: volume)
                            }
                        }
                    }

                    Section(header: Text("Unused")) {
                        ForEach(filteredVolumes) { volume in
                            if !isMounted(volume) {
                                DockerVolumeItem(volume: volume)
                            }
                        }
                    }

                    if filteredVolumes.isEmpty {
                        HStack {
                            Spacer()
                            Text("No volumes")
                                    .font(.title)
                                    .foregroundColor(.secondary)
                                    .padding(.top, 32)
                            Spacer()
                        }
                    } else {
                        HStack {
                            Spacer()
                            VStack {
                                Text("You can also find these volumes at ~/\(Folders.nfsName)/docker.")
                                        .font(.title3)
                                        .foregroundColor(.secondary)
                            }
                                    .padding(.vertical, 24)
                            Spacer()
                        }
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

        // will cause feedback loop if docker is stopped
        // querying this will start it
        if let containers = vmModel.containers,
           let dockerContainer = containers.first(where: { $0.name == "docker" }),
           dockerContainer.running {
            await vmModel.tryRefreshDockerList()
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
}
