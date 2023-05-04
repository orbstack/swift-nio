//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerImagesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?

    var body: some View {
        DockerStateWrapperView(
            refreshAction: refresh
        ) { _, _ in
            if let images = vmModel.dockerImages {
                List(selection: $selection) {
                    Section(header: Text("Tagged")) {
                        ForEach(images) { image in
                            if image.hasTag {
                                DockerImageItem(image: image)
                            }
                        }
                    }

                    if images.isEmpty {
                        HStack {
                            Spacer()
                            Text("No images")
                                    .font(.title)
                                    .foregroundColor(.secondary)
                                    .padding(.top, 32)
                            Spacer()
                        }
                    }

                    Section(header: Text("Untagged")) {
                        ForEach(images) { image in
                            if !image.hasTag {
                                DockerImageItem(image: image)
                            }
                        }
                    }
                }
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
        .navigationTitle("Images")
    }

    private func refresh() async {
        await vmModel.tryRefreshList()

        // will cause feedback loop if docker is stopped
        // querying this will start it
        if let containers = vmModel.containers,
           let dockerContainer = containers.first(where: { $0.name == "docker" }),
           dockerContainer.running {
            await vmModel.tryRefreshDockerList(doContainers: true, doImages: true)
        }
    }
}
