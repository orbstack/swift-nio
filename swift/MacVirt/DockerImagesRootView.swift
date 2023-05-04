//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerImagesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: String?
    @State private var searchQuery: String = ""

    var body: some View {
        DockerStateWrapperView(
            refreshAction: refresh
        ) { _, _ in
            if let images = vmModel.dockerImages {
                let filteredImages = images.filter { image in
                    searchQuery.isEmpty ||
                            image.id.localizedCaseInsensitiveContains(searchQuery) ||
                            image.repoTags?.first(where: { $0.localizedCaseInsensitiveContains(searchQuery) }) != nil
                }
                let imageCount = filteredImages.count
                let totalSize = filteredImages.reduce(0) { $0 + $1.size }
                let totalSizeFormatted = ByteCountFormatter.string(fromByteCount: Int64(totalSize), countStyle: .file)

                List(selection: $selection) {
                    Section(header: Text("Tagged")) {
                        ForEach(filteredImages) { image in
                            if image.hasTag {
                                DockerImageItem(image: image)
                            }
                        }
                    }

                    if filteredImages.isEmpty {
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
                        ForEach(filteredImages) { image in
                            if !image.hasTag {
                                DockerImageItem(image: image)
                            }
                        }
                    }
                }
                .navigationSubtitle("\(totalSizeFormatted) used")
            } else {
                ProgressView(label: {
                    Text("Loading")
                })
            }
        }
        .navigationTitle("Images")
        .searchable(
            text: $searchQuery,
            placement: .toolbar,
            prompt: "Search"
        )
    }

    private func refresh() async {
        await vmModel.tryRefreshList()

        // will cause feedback loop if docker is stopped
        // querying this will start it
        if let containers = vmModel.containers,
           let dockerContainer = containers.first(where: { $0.id == ContainerIds.docker }),
           dockerContainer.running {
            await vmModel.tryRefreshDockerList()
        }
    }
}
