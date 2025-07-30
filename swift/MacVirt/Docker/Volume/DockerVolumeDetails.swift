//
// Created by Danny Lin on 1/28/24.
//

import Foundation
import SwiftUI

struct DockerVolumeDetails: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var windowTracker: WindowTracker
    @EnvironmentObject var actionTracker: ActionTracker

    @StateObject private var windowHolder = WindowHolder()

    let volume: DKVolume

    var body: some View {
        DetailsStack {
            DetailsKvSection {
                let showMountpoint =
                    volume.mountpoint != "/var/lib/docker/volumes/\(volume.name)/_data"

                DetailsRow("Name", copyableText: volume.name)
                DetailsRow("Created", text: "\(volume.formattedCreatedAt) (\(volume.formattedCreatedAtLong))")

                if let usageData = vmModel.dockerDf?.volumes[volume.name]?.usageData {
                    let fmtSize = ByteCountFormatter.string(
                        fromByteCount: usageData.size, countStyle: .file)
                    DetailsRow("Size", text: fmtSize)
                } else {
                    DetailsRow("Size", text: "Calculating…")
                }

                if showMountpoint {
                    DetailsRow("Mountpoint") {
                        Text("\(volume.mountpoint)")
                            .font(.body.monospaced())
                    }
                }

                if volume.driver != "local" {
                    DetailsRow("Driver", text: volume.driver)
                }
                if volume.scope != "local" {
                    DetailsRow("Scope", text: volume.scope)
                }
            }

            let usedByContainers = vmModel.dockerContainers?
                .byId
                .values
                .lazy
                .filter {
                    $0.mounts.contains(where: { $0.type == .volume && $0.name == volume.name })
                }
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

            if let labels = volume.labels,
                !labels.isEmpty
            {
                DetailsLabelsSection(labels: labels)
            }

            if let options = volume.options,
                !options.isEmpty
            {
                DetailsLabelsSection("Options", labels: options)
            }

            DetailsButtonSection {
                DetailsButton {
                    volume.openExportPanel(
                        windowHolder: windowHolder,
                        actionTracker: actionTracker,
                        vmModel: vmModel
                    )
                } label: {
                    Label("Export", systemImage: "square.and.arrow.up")
                }
            }
        }
        .windowHolder(windowHolder)
    }
}

extension DKVolume {
    var nfsPath: String {
        "\(Folders.nfsDockerVolumes)/\(name)"
    }

    func openNfsDirectory() {
        NSWorkspace.openFolder(nfsPath)
    }

    func openExportPanel(
        windowHolder: WindowHolder,
        actionTracker: ActionTracker,
        vmModel: VmViewModel
    ) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "\(self.name).tar.zst"

        let window = windowHolder.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                Task {
                    await actionTracker.withVolumeExport(id: self.id) {
                        await vmModel.tryDockerExportVolume(volumeId: self.id, hostPath: url.path)
                    }
                }
            }
        }
    }
}
