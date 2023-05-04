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

struct DockerImageItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var image: DKImage

    @State private var actionInProgress = false
    @State private var presentConfirmDelete = false

    var body: some View {
        HStack {
            HStack {
                let color = colors[image.id.hashValue %% colors.count]
                Image(systemName: "doc.zipper")
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .frame(width: 32, height: 32)
                        .padding(.trailing, 8)
                        .foregroundColor(color)

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
            .disabled(actionInProgress)
            .help("Delete image")
        }
        .padding(.vertical, 4)
        .confirmationDialog("Delete \(image.userTag)?",
                isPresented: $presentConfirmDelete) {
            Button("Delete", role: .destructive) {
                Task { @MainActor in
                    actionInProgress = true
                    await vmModel.tryDockerImageRemove(image.id)
                    actionInProgress = false
                }
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .contextMenu {
            Button(action: {
                self.presentConfirmDelete = true
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
}