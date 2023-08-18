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
                // exclude size of layers shared w/ other images
                let totalSize = filteredImages.reduce(0) { $0 + $1.size - $1.sharedSize }
                let totalSizeFormatted = ByteCountFormatter.string(fromByteCount: Int64(totalSize), countStyle: .file)

                let taggedImages = filteredImages.filter { $0.hasTag }
                let untaggedImages = filteredImages.filter { !$0.hasTag }

                // 0 spacing to fix bg color gap between list and getting started hint
                VStack(spacing: 0) {
                    if !filteredImages.isEmpty {
                        List(selection: $selection) {
                            Section(header: Text("Tagged")) {
                                ForEach(taggedImages) { image in
                                    DockerImageItem(image: image, selection: selection, isFirstInList: image.id == taggedImages.first?.id)
                                    .equatable()
                                }
                            }

                            Section(header: Text("Untagged")) {
                                ForEach(untaggedImages) { image in
                                    DockerImageItem(image: image, selection: selection, isFirstInList: false)
                                    .equatable()
                                }
                            }
                        }
                        .navigationSubtitle("\(totalSizeFormatted) total")
                    } else {
                        Spacer()

                        HStack {
                            Spacer()
                            if searchQuery.isEmpty {
                                ContentUnavailableViewCompat("No Images", systemImage: "doc.zipper")
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
        .navigationTitle("Images")
        .searchable(
            text: $searchQuery,
            placement: .toolbar,
            prompt: "Search"
        )
    }

    private func refresh() async {
        await vmModel.tryRefreshList()
        await vmModel.maybeTryRefreshDockerList()
    }
}
