//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerImageItem: View, Equatable {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var image: DKImage
    var selection: Set<String>

    static func == (lhs: DockerImageItem, rhs: DockerImageItem) -> Bool {
        lhs.image.id == rhs.image.id &&
                lhs.selection == rhs.selection
    }

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(image: image) != nil
        let isInUse = isInUse()

        HStack {
            HStack {
                VStack(alignment: .leading) {
                    Text(image.userTag)
                            .font(.body)
                            .truncationMode(.tail)
                            .lineLimit(1)

                    Text("\(image.formattedSize), created \(image.formattedCreated)")
                            .font(.subheadline)
                            .foregroundColor(.secondary)
                }
            }
            Spacer()

            if !(image.repoTags?.isEmpty ?? true) {
                Button(action: {
                    openFolder()
                }) {
                    Image(systemName: "folder.fill")
                            // match ProgressIconButton size
                    .frame(width: 24, height: 24)
                }
                .buttonStyle(.borderless)
                .disabled(actionInProgress)
                .help("Open image")
            }

            ProgressIconButton(systemImage: "trash.fill",
                    actionInProgress: actionInProgress,
                    role: .destructive) {
                finishDelete()
            }
            .disabled(actionInProgress || isInUse)
            .help(isInUse ? "Image in use" : "Delete image")
        }
        .padding(.vertical, 4)
        .contextMenu {
            Button(action: {
                openFolder()
            }) {
                Label("Open", systemImage: "folder")
            }.disabled(actionInProgress || (image.repoTags?.isEmpty ?? true))

            Divider()

            Button(action: {
                finishDelete()
            }) {
                Label("Delete", systemImage: "trash.fill")
            }.disabled(actionInProgress || isInUse)

            Divider()

            Button(action: {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(image.userTag, forType: .string)
            }) {
                Label("Copy Tag", systemImage: "doc.on.doc")
            }

            Button(action: {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(image.userId, forType: .string)
            }) {
                Label("Copy ID", systemImage: "doc.on.doc")
            }
        }
    }

    private func finishDelete() {
        for id in resolveActionList() {
            NSLog("remove image \(id)")
            Task { @MainActor in
                await actionTracker.with(imageId: id, action: .remove) {
                    await vmModel.tryDockerImageRemove(id)
                }
            }
        }
    }

    private func isInUse() -> Bool {
        if let containers = vmModel.dockerContainers {
            // we use force image delete, so stopped containers are auto-removed
            return containers.contains(where: { $0.imageId == image.id && $0.running })
        } else {
            return false
        }
    }

    private func isSelected() -> Bool {
        selection.contains(image.id)
    }

    private func resolveActionList() -> Set<String> {
        // if action is performed on a selected item, then use all selections
        // otherwise only use volume
        if isSelected() {
            // SwiftUI List bug: deleted items stay in selection set so we need to filter
            if let images = vmModel.dockerImages {
                return selection.filter { sel in
                    images.contains(where: { $0.id == sel })
                }
            } else {
                return selection
            }
        } else {
            return [image.id]
        }
    }

    private func openFolder() {
        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: "\(Folders.nfsDockerImages)/\(image.userTag)")
    }
}