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
            DetailsSection("Info") {
                let showMountpoint =
                    volume.mountpoint != "/var/lib/docker/volumes/\(volume.name)/_data"
                SimpleKvTable(longestLabel: showMountpoint ? "Mountpoint" : "Created") {
                    SimpleKvTableRow("Name") {  
                        CopyableText(volume.name)
                    }

                    SimpleKvTableRow("Created") {
                        Text(volume.formattedCreatedAt)
                    }

                    if let dockerDf = vmModel.dockerSystemDf,
                        let dfVolume = dockerDf.volumes.first(where: { $0.name == volume.name }),
                        let usageData = dfVolume.usageData
                    {
                        let fmtSize = ByteCountFormatter.string(
                            fromByteCount: usageData.size, countStyle: .file)
                        SimpleKvTableRow("Size") {
                            Text(fmtSize)
                        }
                    }

                    if showMountpoint {
                        SimpleKvTableRow("Mountpoint") {
                            Text("\(volume.mountpoint)")
                                .font(.body.monospaced())
                        }
                    }

                    if volume.driver != "local" {
                        SimpleKvTableRow("Driver") {
                            Text(volume.driver)
                        }
                    }
                    if volume.scope != "local" {
                        SimpleKvTableRow("Scope") {
                            Text(volume.scope)
                        }
                    }
                }
            }

            let usedByContainers = vmModel.dockerContainers?
                .lazy
                .filter {
                    $0.mounts.contains(where: { $0.type == .volume && $0.name == volume.name })
                }
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

            if let labels = volume.labels,
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

            if let options = volume.options,
                !options.isEmpty
            {
                ScrollableDetailsSection("Options") {
                    AlignedSimpleKvTable {
                        let sortedOptions = options.sorted { $0.key < $1.key }
                        ForEach(sortedOptions, id: \.key) { key, value in
                            AlignedSimpleKvTableRow(key) {
                                CopyableText(value)
                            }
                        }
                    }
                }
            }

            DividedButtonStack {
                DividedRowButton {
                    volume.openNfsDirectory()
                } label: {
                    Label("Files", systemImage: "folder")
                }

                DividedRowButton {
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
    func openNfsDirectory() {
        NSWorkspace.openFolder("\(Folders.nfsDockerVolumes)/\(name)")
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
