//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachineContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel

    var record: ContainerRecord

    @State private var actionInProgress = false
    @State private var presentConfirmDelete = false

    var body: some View {
        HStack {
            Image("distro_\(record.image.distro)")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 32, height: 32)
                    .padding(.trailing, 8)
                    .opacity(record.running ? 1 : 0.5)
            VStack(alignment: .leading) {
                Text(record.name)
                        .font(.body)
                Text("\(record.image.version), \(record.image.arch)")
                        .font(.subheadline)
                        .foregroundColor(.secondary)
            }
            .opacity(record.running ? 1 : 0.5)

            Spacer()

            let progressOpacity = (actionInProgress || record.state == .creating) ? 1.0 : 0.0
            if record.running {
                Button(action: {
                    Task { @MainActor in
                        actionInProgress = true
                        await vmModel.tryStopContainer(record)
                        actionInProgress = false
                    }
                }) {
                    ZStack {
                        Image(systemName: "stop.fill")
                                .opacity(1 - progressOpacity)

                        ProgressView()
                                .scaleEffect(0.75)
                                .opacity(progressOpacity)
                    }
                }
                        .buttonStyle(.borderless)
                        .disabled(actionInProgress)
                        .help("Stop \(record.name)")
            } else {
                Button(action: {
                    Task { @MainActor in
                        actionInProgress = true
                        await vmModel.tryStartContainer(record)
                        actionInProgress = false
                    }
                }) {
                    ZStack {
                        Image(systemName: "play.fill")
                                .opacity(1 - progressOpacity)

                        ProgressView()
                                .scaleEffect(0.75)
                                .opacity(progressOpacity)
                    }
                }
                .buttonStyle(.borderless)
                .disabled(actionInProgress || record.state == .creating)
                .help("Start \(record.name)")
            }
        }
        .padding(.vertical, 4)
        .contextMenu {
            Button(action: {
                openInTerminal()
            }) {
                Label("Open Terminal", systemImage: "terminal")
            }
            Button(action: {
                Task {
                    await vmModel.trySetDefaultContainer(record)
                }
            }) {
                Label("Make Default", systemImage: "star")
            }
            Divider()
            Button(action: {
                Task { @MainActor in
                    actionInProgress = true
                    await vmModel.tryRestartContainer(record)
                    actionInProgress = false
                }
            }) {
                Label("Restart", systemImage: "restart")
            }
            .disabled(actionInProgress)
            Divider()
            Button(role: .destructive, action: {
                if CGKeyCode.optionKeyPressed {
                    finishDelete()
                } else {
                    self.presentConfirmDelete = true
                }
            }) {
                Label("Delete", systemImage: "trash")
            }
            .disabled(actionInProgress)
        }
        .confirmationDialog("Delete \(record.name)?",
                isPresented: $presentConfirmDelete) {
            Button("Delete", role: .destructive) {
                finishDelete()
            }
        } message: {
            Text("Data will be permanently lost.")
        }
        .onDoubleClick {
            openInTerminal()
        }
    }

    private func openInTerminal() {
        Task {
            do {
                try await openTerminal(AppConfig.shellExe, ["-m", record.name])
            } catch {
                NSLog("Open terminal failed: \(error)")
            }
        }
    }

    private func finishDelete() {
        Task { @MainActor in
            actionInProgress = true
            await vmModel.tryDeleteContainer(record)
            actionInProgress = false
        }
    }
}