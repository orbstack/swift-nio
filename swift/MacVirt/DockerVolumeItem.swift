//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

fileprivate let colors = [
    Color(.systemRed),
    Color(.systemGreen),
    Color(.systemBlue),
    Color(.systemOrange),
    Color(.systemYellow),
    Color(.systemBrown),
    Color(.systemPink),
    Color(.systemPurple),
    Color(.systemGray),
    Color(.systemTeal),
    Color(.systemIndigo),
    Color(.systemMint),
    Color(.systemCyan),
]

struct DockerVolumeItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var volume: DKVolume

    @State private var actionInProgress = false

    var body: some View {
        HStack {
            HStack {
                let color = colors[volume.name.hashValue %% colors.count]
                Image(systemName: "externaldrive.fill")
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 32, height: 32)
                        .padding(.trailing, 8)
                        .foregroundColor(color)

                VStack(alignment: .leading) {
                    Text(volume.name)
                            .font(.body)

                    // TODO: subheadline = size
                }
            }
            Spacer()

            Button(action: {
                Task { @MainActor in
                    actionInProgress = true
                    await vmModel.tryDockerVolumeRemove(volume.name)
                    actionInProgress = false
                }
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
                    .help("Delete volume")
        }
        .padding(.vertical, 4)
        .onDoubleClick {
            openFolder()
        }
        .contextMenu {
            Button(action: {
                Task { @MainActor in
                    actionInProgress = true
                    await vmModel.tryDockerVolumeRemove(volume.name)
                    actionInProgress = false
                }
            }) {
                Label("Delete", systemImage: "trash.fill")
            }.disabled(actionInProgress)

            Divider()

            Button(action: {
                openFolder()
            }) {
                Label("Open in Finder", systemImage: "terminal")
            }

            Divider()

            Button(action: {
                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(volume.name, forType: .string)
            }) {
                Label("Copy Name", systemImage: "doc.on.doc")
            }

            Button(action: {
                let home = FileManager.default.homeDirectoryForCurrentUser.path
                let path = home + "/Linux/docker/volumes/\(volume.name)"

                let pasteboard = NSPasteboard.general
                pasteboard.clearContents()
                pasteboard.setString(path, forType: .string)
            }) {
                Label("Copy Path", systemImage: "doc.on.doc")
            }
        }
    }

    private func openFolder() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: home + "/Linux/docker/volumes/\(volume.name)")
    }
}