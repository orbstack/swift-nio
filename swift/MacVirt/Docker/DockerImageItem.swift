//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI
import Defaults

struct DockerImageItem: View, Equatable {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    @Default(.tipsImageMountsShow) private var tipsImageMountsShow

    var image: DKImage
    var selection: Set<String>
    var isFirstInList: Bool

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

            if image.repoTags?.isEmpty == false {
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
                .if(isFirstInList) {
                    $0.popover(isPresented: $tipsImageMountsShow, arrowEdge: .leading) {
                        HStack {
                            Image(systemName: "folder.circle")
                                .resizable()
                                .frame(width: 32, height: 32)
                                .foregroundColor(.accentColor)
                                .padding(.trailing, 4)

                            VStack(alignment: .leading, spacing: 2) {
                                Text("New: Direct image access")
                                    .font(.headline)

                                Text("Explore image files in Finder and other tools")
                                    .font(.body)
                                    .foregroundColor(.secondary)
                            }
                        }
                        .padding(20)
                        .overlay(alignment: .topLeading) { // opposite side of arrow edge
                            Button(action: {
                                tipsImageMountsShow = false
                            }) {
                                Image(systemName: "xmark")
                                    .resizable()
                                    .frame(width: 8, height: 8)
                                    .foregroundColor(.secondary)
                            }
                            .buttonStyle(.plain)
                            .padding(8)
                        }
                    }
                }
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
        .onRawDoubleClick {
            if image.repoTags?.isEmpty == false {
                openFolder()
            }
        }
        .contextMenu {
            Button(action: {
                openFolder()
            }) {
                Label("Open", systemImage: "folder")
            }.disabled(actionInProgress || (image.repoTags?.isEmpty != false))

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
                await actionTracker.with(imageId: id, action: .delete) {
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