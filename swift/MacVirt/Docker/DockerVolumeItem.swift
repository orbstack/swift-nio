//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerVolumeItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var volume: DKVolume
    var isMounted: Bool
    var selection: Set<String>

    @State private var presentConfirmDelete = false

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(volume: volume) != nil

        let deletionList = resolveActionList()
        let deleteConfirmMsg = deletionList.count > 1 ?
                "Delete \(deletionList.count) volumes?" :
                "Delete \(deletionList.joined())?"

        HStack {
            HStack {
                let color = SystemColors.forString(volume.name)
                Image(systemName: "externaldrive.fill")
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 32, height: 32)
                        .padding(.trailing, 8)
                        .foregroundColor(color)

                VStack(alignment: .leading) {
                    Text(volume.name)
                            .font(.body)
                            .truncationMode(.tail)
                            .lineLimit(1)

                    // can we find the size from system df?
                    if let dockerDf = vmModel.dockerSystemDf,
                       let dfVolume = dockerDf.volumes.first(where: { $0.name == volume.name }),
                       let usageData = dfVolume.usageData {
                        let fmtSize = ByteCountFormatter.string(fromByteCount: usageData.size, countStyle: .file)
                        Text("\(fmtSize), created \(volume.formattedCreatedAt)")
                                .font(.subheadline)
                                .foregroundColor(.secondary)
                    } else {
                        Text("Created \(volume.formattedCreatedAt)")
                                .font(.subheadline)
                                .foregroundColor(.secondary)
                    }
                }
            }
            Spacer()

            Button(action: {
                openFolder()
            }) {
                Image(systemName: "folder.fill")
                // match ProgressIconButton size
                .frame(width: 24, height: 24)
            }
            .buttonStyle(.borderless)
            .disabled(actionInProgress)
            .help("Open volume")

            ProgressIconButton(systemImage: "trash.fill",
                    actionInProgress: actionInProgress) {
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
        .padding(.vertical, 8)
        .akListOnDoubleClick {
            openFolder()
        }
        .confirmationDialog(deleteConfirmMsg,
                isPresented: $presentConfirmDelete) {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .akListContextMenu {
            Button(action: {
                openFolder()
            }) {
                Label("Open", systemImage: "folder")
            }

            Divider()

            Button(action: {
                // skip confirmation if Option pressed
                if CGKeyCode.optionKeyPressed {
                    finishDelete()
                } else {
                    self.presentConfirmDelete = true
                }
            }) {
                Label("Delete", systemImage: "trash.fill")
            }.disabled(actionInProgress || isMounted)

            Divider()

            Button(action: {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(volume.name, forType: .string)
            }) {
                Label("Copy Name", systemImage: "doc.on.doc")
            }

            Button(action: {
                let path = "\(Folders.nfsDockerVolumes)/\(volume.name)"

                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(path, forType: .string)
            }) {
                Label("Copy Path", systemImage: "doc.on.doc")
            }
        }
    }

    private func openFolder() {
        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: "\(Folders.nfsDockerVolumes)/\(volume.name)")
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
            // SwiftUI List bug: deleted items stay in selection set so we need to filter
            if let volumes = vmModel.dockerVolumes {
                return selection.filter { sel in
                    volumes.contains(where: { $0.name == sel })
                }
            } else {
                return selection
            }
        } else {
            return [volume.name]
        }
    }
}