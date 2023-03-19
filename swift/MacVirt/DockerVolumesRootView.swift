//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerVolumesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?

    var body: some View {
        DockerStateWrapperView(
            refreshAction: refresh
        ) { _, _ in
            if let volumes = vmModel.dockerVolumes {
                List(selection: $selection) {
                    ForEach(volumes) { volume in
                        DockerVolumeItem(volume: volume)
                    }

                    if volumes.isEmpty {
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
                                Text("You can also find these volumes in ~/Linux/docker.")
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
}
