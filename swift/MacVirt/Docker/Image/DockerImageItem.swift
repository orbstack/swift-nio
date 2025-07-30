//
// Created by Danny Lin on 2/5/23.
//

import Defaults
import Foundation
import SwiftUI

struct DockerImageItem: View, Equatable {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var listModel: AKListModel

    @StateObject private var windowHolder = WindowHolder()
    @State private var showEmulationAlert = false

    @Default(.tipsImageMountsShow) private var tipsImageMountsShow

    var image: DKSummaryAndFullImage
    var selection: Set<String> {
        listModel.selection as! Set<String>
    }

    var isFirstInList: Bool

    static func == (lhs: DockerImageItem, rhs: DockerImageItem) -> Bool {
        lhs.image.id == rhs.image.id
    }

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(image: image) != nil
        let isInUse = isInUse()

        HStack {
            DockerImageIcon(image: image)

            VStack(alignment: .leading) {
                let userTag = image.summary.userTag
                Text(userTag)
                    .font(.body)
                    // end of image tag is more important
                    .truncationMode(.head)
                    .lineLimit(1)
                    .help(userTag)

                Text(
                    "\(image.summary.formattedSize), \(image.summary.formattedCreated)"
                )
                .font(.subheadline)
                .foregroundColor(.secondary)
            }

            Spacer()

            ProgressButtonRow {
                if !AppConfig.nativeArchs.contains(image.full.architecture) {
                    Button {
                        showEmulationAlert = true
                    } label: {
                        Text(image.full.architecture)
                            .font(.caption)
                            // TODO: this is bad...
                            .foregroundStyle(.black.opacity(0.8))
                            .padding(.horizontal, 4)
                            .padding(.vertical, 2)
                            // TODO: composite onto bg so list highlight doesn't affect it
                            .background(SystemColors.desaturate(Color(.systemYellow)), in: .capsule)
                    }
                    .buttonStyle(.plain)
                    .help("Runs slower due to emulation")
                    .akAlert(isPresented: $showEmulationAlert) {
                        "Runs slower due to emulation"
                        "This image was built for a different architecture (\(image.full.architecture)), so it needs to be emulated on your machine. This means it will run slower and may have more bugs."
                    }
                }

                ProgressIconButton(
                    systemImage: "trash.fill",
                    actionInProgress: actionInProgress,
                    role: .destructive
                ) {
                    finishDelete()
                }
                .disabled(actionInProgress || isInUse)
                .help(isInUse ? "Image in use" : "Delete")
            }
        }
        .padding(.vertical, 4)
        .akListOnDoubleClick {
            if image.summary.repoTags?.isEmpty == false {
                image.summary.openFolder()
            }
        }
        .akListContextMenu {
            Button {
                image.summary.openFolder()
            } label: {
                Label("Show in Finder", systemImage: "folder")
            }.disabled(actionInProgress || (image.summary.repoTags?.isEmpty != false))

            Button {
                if vmModel.isLicensed {
                    image.summary.openDebugShell()
                } else {
                    vmModel.presentRequiresLicense = true
                }
            } label: {
                Label("Debug Shell", systemImage: "ladybug")
            }.disabled(actionInProgress)

            Button {
                image.summary.openExportPanel(
                    windowHolder: windowHolder,
                    actionTracker: actionTracker,
                    vmModel: vmModel
                )
            } label: {
                Label("Export", systemImage: "square.and.arrow.up")
            }.disabled(actionInProgress)

            Divider()

            Button {
                finishDelete()
            } label: {
                Label("Delete", systemImage: "trash")
            }.disabled(actionInProgress || isInUse)

            Divider()

            Button {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(image.summary.userTag, forType: .string)
            } label: {
                Label("Copy Tag", systemImage: "doc.on.doc")
            }

            Button {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(image.summary.userId, forType: .string)
            } label: {
                Label("Copy ID", systemImage: "doc.on.doc")
            }

            Button {
                NSPasteboard.copy("\(Folders.nfsDockerImages)/\(image.summary.userTag)")
            } label: {
                Label("Copy Path", systemImage: "doc.on.doc")
            }
        }
        .windowHolder(windowHolder)
    }

    private func finishDelete() {
        for id in resolveActionList() {
            NSLog("remove image \(id)")
            Task { @MainActor in
                await actionTracker.with(imageId: id, action: .delete) {
                    await vmModel.tryDockerImageRemove(id)
                }
            }
        }
    }

    // slightly different logic from vmModel.usedImageIds:
    // this doesn't count stopped containers
    private func isInUse() -> Bool {
        // we use force image delete, so stopped containers are auto-removed
        return vmModel.dockerContainers?.byId.values.contains {
            $0.imageId == image.id && $0.running
        } ?? false
    }

    private func isSelected() -> Bool {
        selection.contains(image.id)
    }

    private func resolveActionList() -> Set<String> {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            return selection
        } else {
            return [image.id]
        }
    }
}

extension DKImage {
    func openExportPanel(
        windowHolder: WindowHolder,
        actionTracker: ActionTracker,
        vmModel: VmViewModel
    ) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "\(self.userTag.replacingOccurrences(of: "/", with: "_")).tar"

        let window = windowHolder.window ?? NSApp.keyWindow ?? NSApp.windows.first!
        panel.beginSheetModal(for: window) { result in
            if result == .OK,
                let url = panel.url
            {
                Task {
                    await actionTracker.with(imageId: self.id, action: .exporting) {
                        let identifier = self.hasTag ? self.userTag : self.id
                        await vmModel.dockerExportImage(id: identifier, url: url)
                    }
                }
            }
        }
    }
}
