//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerImagesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel

    @State private var selection: Set<String> = []
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
                let totalSize = filteredImages.reduce(0) { $0 + $1.size }
                let totalSizeFormatted = ByteCountFormatter.string(fromByteCount: Int64(totalSize), countStyle: .file)

                // 0 spacing to fix bg color gap between list and getting started hint
                VStack(spacing: 0) {
                    if !filteredImages.isEmpty {
                        List(selection: $selection) {
                            Section(header: Text("Tagged")) {
                                ForEach(filteredImages) { image in
                                    if image.hasTag {
                                        DockerImageItem(image: image, selection: selection)
                                    }
                                }
                            }

                            Section(header: Text("Untagged")) {
                                ForEach(filteredImages) { image in
                                    if !image.hasTag {
                                        DockerImageItem(image: image, selection: selection)
                                    }
                                }
                            }
                        }
                        .navigationSubtitle("\(totalSizeFormatted) used")
                    } else {
                        Spacer()

                        HStack {
                            Spacer()
                            VStack {
                                Text("No images")
                                        .font(.title)
                                        .foregroundColor(.secondary)
                            }
                                    .padding(.top, 32)
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
           dockerContainer.state != .stopped {
            await vmModel.tryRefreshDockerList()
        }
    }
}
