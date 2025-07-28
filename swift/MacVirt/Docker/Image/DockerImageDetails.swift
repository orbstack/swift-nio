//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DockerImageDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var actionTracker: ActionTracker

    @StateObject private var windowHolder = WindowHolder()

    let image: DKSummaryAndFullImage

    var body: some View {
        DetailsStack {
            DetailsKvSection {
                DetailsRow("ID") {
                    CopyableText(
                        String(image.id.trimmingPrefix("sha256:").prefix(12)), copyAs: image.id
                    )
                    .font(.body.monospaced())
                }

                DetailsRow("Created", text: image.summary.formattedCreated)
                DetailsRow("Size", text: image.summary.formattedSize)
                DetailsRow("Platform", text: image.full.architecture)
            }

            DetailsButtonSection {
                DetailsButton {
                    image.summary.openExportPanel(
                        windowHolder: windowHolder,
                        actionTracker: actionTracker,
                        vmModel: vmModel
                    )
                } label: {
                    Label("Export", systemImage: "square.and.arrow.up")
                }
            }

            if let tags = image.summary.repoTags,
                !tags.isEmpty
            {
                DetailsListSection("Tags") {
                    ForEach(tags, id: \.self) { tag in
                        CopyableText(tag)
                    }
                }
            }

            let usedByContainers = vmModel.dockerContainers?
                .byId
                .values
                .lazy
                .filter { $0.imageId == image.id }
                .sorted { $0.userName < $1.userName }
            if let usedByContainers,
                !usedByContainers.isEmpty
            {
                DetailsListSection("Used By") {
                    ForEach(usedByContainers) { container in
                        Text(container.userName)
                    }
                }
            }

            if let labels = image.summary.labels,
                !labels.isEmpty
            {
                let sortedLabels = labels.sorted { $0.key < $1.key }.map {
                    KeyValueItem(key: $0.key, value: $0.value)
                }
                DetailsKvTableSection("Labels", items: sortedLabels)
            }
        }
        .windowHolder(windowHolder)
    }
}
