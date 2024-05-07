//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerImagesRootView: View {
    @EnvironmentObject private var vmModel: VmViewModel
    @EnvironmentObject private var windowTracker: WindowTracker
    @EnvironmentObject private var actionTracker: ActionTracker

    @State private var selection: Set<String> = []

    var body: some View {
        let searchQuery = vmModel.searchText

        DockerStateWrapperView(\.dockerImages) { images, _ in
            let filteredImages = images.filter { (image: DKSummaryAndFullImage) in
                searchQuery.isEmpty ||
                image.id.localizedCaseInsensitiveContains(searchQuery) ||
                image.summary.repoTags?.first(where: { $0.localizedCaseInsensitiveContains(searchQuery) }) != nil
            }

            // 0 spacing to fix bg color gap between list and getting started hint
            VStack(spacing: 0) {
                if !filteredImages.isEmpty {
                    // exclude size of layers shared w/ other images
                    let totalSize = filteredImages.reduce(0) { $0 + $1.summary.size - $1.summary.sharedSize }
                    let totalSizeFormatted = ByteCountFormatter.string(fromByteCount: Int64(totalSize), countStyle: .file)

                    let usedImageIds = vmModel.usedImageIds()
                    let usedImages = filteredImages.filter { usedImageIds.contains($0.id) }
                    let unusedImages = filteredImages.filter { !usedImageIds.contains($0.id) && $0.summary.hasTag }
                    let danglingImages = filteredImages.filter { !usedImageIds.contains($0.id) && !$0.summary.hasTag }
                    let listData = [
                        AKSection("In Use", usedImages),
                        AKSection("Unused", unusedImages),
                        AKSection("Dangling", danglingImages),
                    ]

                    // 46 is empirically correct, matches auto height. not sure where it comes from
                    AKList(listData, selection: $selection, rowHeight: 46) { image in
                        DockerImageItem(image: image,
                                        isFirstInList: image.id == usedImages.first?.id)
                            .equatable()
                            .environmentObject(vmModel)
                            .environmentObject(windowTracker)
                            .environmentObject(actionTracker)
                    }
                    .navigationSubtitle("\(totalSizeFormatted) total")
                    .inspectorSelection(selection)
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
    }
}
