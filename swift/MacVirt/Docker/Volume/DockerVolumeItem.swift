//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerVolumeItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker
    @EnvironmentObject var listModel: AKListModel

    @StateObject var windowHolder = WindowHolder()

    @State private var presentConfirmDelete = false

    var volume: DKVolume
    private var selection: Set<String> {
        listModel.selection as! Set<String>
    }

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(volume: volume) != nil
        let isMounted = vmModel.volumeIsMounted(volume)

        let deletionList = resolveActionList()
        let deleteConfirmMsg =
            deletionList.count > 1
            ? "Delete \(deletionList.count) volumes?" : "Delete \(deletionList.joined())?"

        HStack {
            HStack {
                let color = SystemColors.forString(volume.name)
                Image(systemName: "externaldrive.fill")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 28, height: 28)
                    .foregroundColor(color)

                VStack(alignment: .leading) {
                    Text(volume.name)
                        .font(.body)
                        .truncationMode(.tail)
                        .lineLimit(1)

                    // can we find the size from system df?
                    if let usageData = vmModel.dockerDf?.volumes[volume.name]?.usageData {
                        let fmtSize = ByteCountFormatter.string(
                            fromByteCount: usageData.size, countStyle: .file)
                        Text("\(fmtSize)")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                    }
                }
            }
            Spacer()

            ProgressButtonRow {
                ProgressIconButton(
                    systemImage: "trash.fill",
                    actionInProgress: actionInProgress
                ) {
                    // skip confirmation if Option pressed
                    if CGKeyCode.optionKeyPressed {
                        finishDelete()
                    } else {
                        self.presentConfirmDelete = true
                    }
                }
                .disabled(actionInProgress || isMounted)
                .help(isMounted ? "Volume in use" : "Delete volume\n(Option to confirm)")
            }
        }
        .padding(.vertical, 8)
        .akListOnDoubleClick {
            volume.openNfsDirectory()
        }
        .confirmationDialog(
            deleteConfirmMsg,
            isPresented: $presentConfirmDelete
        ) {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .akListContextMenu {
            Button {
                volume.openNfsDirectory()
            } label: {
                Label("Show in Finder", systemImage: "folder")
            }

            Button {
                volume.openExportPanel(
                    windowHolder: windowHolder,
                    actionTracker: actionTracker,
                    vmModel: vmModel
                )
            } label: {
                Label("Export", systemImage: "square.and.arrow.up")
            }

            Divider()

            Button {
                // skip confirmation if Option pressed
                if CGKeyCode.optionKeyPressed {
                    finishDelete()
                } else {
                    self.presentConfirmDelete = true
                }
            } label: {
                Label("Delete", systemImage: "trash")
            }.disabled(actionInProgress || isMounted)

            Divider()

            Button {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(volume.name, forType: .string)
            } label: {
                Label("Copy Name", systemImage: "doc.on.doc")
            }

            Button {
                let path = "\(Folders.nfsDockerVolumes)/\(volume.name)"

                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(path, forType: .string)
            } label: {
                Label("Copy Path", systemImage: "doc.on.doc")
            }
        }
    }

    private func finishDelete() {
        for name in resolveActionList() {
            NSLog("remove volume \(name)")
            Task { @MainActor in
                await actionTracker.with(volumeId: name, action: .delete) {
                    await vmModel.tryDockerVolumeRemove(name)
                }
            }
        }
    }

    private func isSelected() -> Bool {
        selection.contains(volume.name)
    }

    private func resolveActionList() -> Set<String> {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            return selection
        } else {
            return [volume.name]
        }
    }
}
