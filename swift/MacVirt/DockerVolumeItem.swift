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
    var isMounted: Bool

    @State private var actionInProgress = false
    @State private var presentConfirmDelete = false

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
            }
            .buttonStyle(.borderless)
            .disabled(actionInProgress)
            .help("Open volume")

            Button(role: .destructive, action: {
                self.presentConfirmDelete = true
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
            .disabled(actionInProgress || isMounted)
            .help(isMounted ? "Volume in use" : "Delete volume")
        }
        .padding(.vertical, 4)
        .onDoubleClick {
            openFolder()
        }
        .confirmationDialog("Delete \(volume.name)?",
                isPresented: $presentConfirmDelete) {
            Button("Delete", role: .destructive) {
                Task { @MainActor in
                    actionInProgress = true
                    await vmModel.tryDockerVolumeRemove(volume.name)
                    actionInProgress = false
                }
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .contextMenu {
            Button(action: {
                openFolder()
            }) {
                Label("Open", systemImage: "folder")
            }

            Divider()

            Button(action: {
                self.presentConfirmDelete = true
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
}