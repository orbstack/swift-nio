//
// Created by Danny Lin on 2/5/23.
//

import Foundation
import SwiftUI

struct MachineContainerItem: View {
    @EnvironmentObject var vmModel: VmViewModel
    @EnvironmentObject var actionTracker: ActionTracker

    var record: ContainerRecord

    @State private var presentConfirmDelete = false

    var body: some View {
        let actionInProgress = actionTracker.ongoingFor(machine: record) != nil

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
                        await actionTracker.with(machine: record, action: .stop) {
                            await vmModel.tryStopContainer(record)
                        }
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
                        await actionTracker.with(machine: record, action: .start) {
                            await vmModel.tryStartContainer(record)
                        }
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
            if record.running {
                Button(action: {
                    Task { @MainActor in
                        await actionTracker.with(machine: record, action: .stop) {
                            await vmModel.tryStopContainer(record)
                        }
                    }
                }) {
                    Label("Stop", systemImage: "restart")
                }
                .disabled(actionInProgress)
            } else {
                Button(action: {
                    Task { @MainActor in
                        await actionTracker.with(machine: record, action: .start) {
                            await vmModel.tryStartContainer(record)
                        }
                    }
                }) {
                    Label("Start", systemImage: "restart")
                }
                .disabled(actionInProgress)
            }

            Button(action: {
                Task { @MainActor in
                    await actionTracker.with(machine: record, action: .restart) {
                        await vmModel.tryRestartContainer(record)
                    }
                }
            }) {
                Label("Restart", systemImage: "restart")
            }
            .disabled(actionInProgress)

            Divider()

            Button(action: {
                Task {
                    await record.openInTerminal()
                }
            }) {
                Label("Open Terminal", systemImage: "terminal")
            }
            Button(action: {
                record.openNfsDirectory()
            }) {
                Label("Open Files", systemImage: "folder")
            }

            Divider()

            Button(action: {
                Task {
                    await vmModel.trySetDefaultContainer(record)
                }
            }) {
                Label("Make Default", systemImage: "star")
            }

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
            Task {
                await record.openInTerminal()
            }
        }
    }

    private func finishDelete() {
        Task { @MainActor in
            await actionTracker.with(machine: record, action: .delete) {
                await vmModel.tryDeleteContainer(record)
            }
        }
    }
}