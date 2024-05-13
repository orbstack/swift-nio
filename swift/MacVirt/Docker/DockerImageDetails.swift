//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DockerImageDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker

    let image: DKSummaryAndFullImage

    var body: some View {
        DetailsStack {
            DetailsSection("Info") {
                // TODO: fix width constraints
                SimpleKvTable(longestLabel: "Architecture") {
                    SimpleKvTableRow("ID") {
                        CopyableText(image.id)
                    }

                    SimpleKvTableRow("Created") {
                        Text(image.summary.formattedCreated)
                    }

                    SimpleKvTableRow("Size") {
                        Text(image.summary.formattedSize)
                    }

                    SimpleKvTableRow("Architecture") {
                        Text(image.full.architecture)
                    }
                }
            }

            if let tags = image.summary.repoTags,
               !tags.isEmpty
            {
                DetailsSection("Tags") {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(tags, id: \.self) { tag in
                            CopyableText(tag)
                        }
                    }
                }
            }

            let usedByContainers = vmModel.dockerContainers?
                .lazy
                .filter { $0.imageId == image.id }
                .sorted { $0.userName < $1.userName }
            if let usedByContainers,
               !usedByContainers.isEmpty
            {
                DetailsSection("Used By") {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(usedByContainers) { container in
                            Text(container.userName)
                        }
                    }
                }
            }

            if let labels = image.summary.labels,
               !labels.isEmpty
            {
                ScrollableDetailsSection("Labels") {
                    AlignedSimpleKvTable {
                        let sortedLabels = labels.sorted { $0.key < $1.key }
                        ForEach(sortedLabels, id: \.key) { key, value in
                            AlignedSimpleKvTableRow(key) {
                                CopyableText(value)
                            }
                        }
                    }
                }
            }
        }
    }
}
