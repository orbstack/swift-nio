//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerImagesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var actionTracker: ActionTracker

    @State private var selection: Set<String> = []
    @State private var searchQuery: String = ""

    var body: some View {
        DockerStateWrapperView(\.dockerImages) { images, _ in
            let filteredImages = images.filter { image in
                searchQuery.isEmpty ||
                        image.id.localizedCaseInsensitiveContains(searchQuery) ||
                        image.repoTags?.first(where: { $0.localizedCaseInsensitiveContains(searchQuery) }) != nil
            }

            // 0 spacing to fix bg color gap between list and getting started hint
            VStack(spacing: 0) {
                if !filteredImages.isEmpty {
                    // exclude size of layers shared w/ other images
                    let totalSize = filteredImages.reduce(0) { $0 + $1.size - $1.sharedSize }
                    let totalSizeFormatted = ByteCountFormatter.string(fromByteCount: Int64(totalSize), countStyle: .file)

                    let taggedImages = filteredImages.filter { $0.hasTag }
                    let untaggedImages = filteredImages.filter { !$0.hasTag }
                    let listData = [
                        AKSection("Tagged", taggedImages),
                        AKSection("Untagged", untaggedImages)
                    ]

                    // 46 is empirically correct, matches auto height. not sure where it comes from
                    AKList(listData, selection: $selection, rowHeight: 46) { image in
                        DockerImageItem(image: image, selection: selection,
                                isFirstInList: image.id == taggedImages.first?.id)
                        .equatable()
                        .environmentObject(vmModel)
                        .environmentObject(actionTracker)
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
        }
        .navigationTitle("Images")
        .searchable(
            text: $searchQuery,
            placement: .toolbar,
            prompt: "Search"
        )
    }
}
