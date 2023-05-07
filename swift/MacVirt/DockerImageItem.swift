//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct DockerImageItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var image: DKImage
    var selection: Set<String>

    var body: some View {
        let actionInProgress = actionTracker.ongoingForImage(image.id) != nil

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

            Button(role: .destructive, action: {
                finishDelete()
            }) {
                let opacity = actionInProgress ? 1.0 : 0.0
                ZStack {
                    Image(systemName: "trash.fill")
                            .opacity(1 - opacity)

                    ProgressView()
                            .scaleEffect(0.75)
                            .opacity(opacity)
                }
            }
            .buttonStyle(.borderless)
            .disabled(actionInProgress)
            .help("Delete image")
        }
        .padding(.vertical, 4)
        .contextMenu {
            Button(action: {
                finishDelete()
            }) {
                Label("Delete", systemImage: "trash.fill")
            }.disabled(actionInProgress)

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
                actionTracker.beginImage(id, action: .remove)
                await vmModel.tryDockerImageRemove(id)
                actionTracker.endImage(id)
            }
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
}