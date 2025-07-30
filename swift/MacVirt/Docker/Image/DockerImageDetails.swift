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

                DetailsRow("Created", text: "\(image.summary.formattedCreated) (\(image.summary.formattedCreatedLong))")
                DetailsRow("Size", text: image.summary.formattedSize)
                DetailsRow("Platform", text: image.full.platform)
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

            DetailsKvSection("Config") {
                if let user = image.full.config?.user,
                    !user.isEmpty
                {
                    DetailsRow("User", text: user)
                }
                if let cmd = image.full.config?.cmd,
                    !cmd.isEmpty
                {
                    DetailsRow("Command") {
                        let joinedCmd = cmd.joined(separator: " ")
                        CopyableText(copyAs: joinedCmd) {
                            Text(joinedCmd)
                                .font(.body.monospaced())
                        }
                    }
                }
                if let entrypoint = image.full.config?.entrypoint,
                    !entrypoint.isEmpty
                {
                    DetailsRow("Entrypoint") {
                        let joinedEntrypoint = entrypoint.joined(separator: " ")
                        CopyableText(copyAs: joinedEntrypoint) {
                            Text(joinedEntrypoint)
                                .font(.body.monospaced())
                        }
                    }
                }
                if let workingDir = image.full.config?.workingDir,
                    !workingDir.isEmpty
                {
                    DetailsRow("Working Directory", text: workingDir)
                }
                if let stopSignal = image.full.config?.stopSignal,
                    !stopSignal.isEmpty
                {
                    DetailsRow("Stop Signal", text: stopSignal)
                }
            }

            if let env = image.full.config?.env,
                !env.isEmpty
            {
                DetailsKvTableSection("Environment", items: env.sorted().map {
                    let parts = $0.split(separator: "=", maxSplits: 1)
                    return KeyValueItem(key: String(parts[0]), value: parts.count == 2 ? String(parts[1]) : "")
                })
            }

            if let labels = image.summary.labels,
                !labels.isEmpty
            {
                let sortedLabels = labels.sorted { $0.key < $1.key }.map {
                    KeyValueItem(key: $0.key, value: $0.value)
                }
                DetailsKvTableSection("Labels", items: sortedLabels)
            }

            if let volumes = image.full.config?.volumes,
                !volumes.isEmpty
            {
                DetailsListSection("Volumes") {
                    ForEach(Array(volumes.keys), id: \.self) { volume in
                        CopyableText(volume)
                    }
                }
            }

            if let exposedPorts = image.full.config?.exposedPorts,
                !exposedPorts.isEmpty
            {
                DetailsListSection("Exposed Ports") {
                    ForEach(Array(exposedPorts.keys), id: \.self) { port in
                        CopyableText(port)
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
        }
        .windowHolder(windowHolder)
    }
}
